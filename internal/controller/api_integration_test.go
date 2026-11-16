//go:build integration

// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	operations "github.com/madhifallah/guarded-batch-operator/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func TestAPISchemaAndReconciliation(t *testing.T) {
	environment := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd", "bases")}, ErrorIfCRDPathMissing: true}
	cfg, err := environment.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := environment.Stop(); err != nil {
			t.Error(err)
		}
	})
	seeded, key := fixture(t, "")
	api, err := client.New(cfg, client.Options{Scheme: seeded.Scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := api.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: key.Namespace}}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"template", "policy", "evidence"} {
		var cm corev1.ConfigMap
		_ = seeded.Get(ctx, client.ObjectKey{Namespace: key.Namespace, Name: name}, &cm)
		cm.ResourceVersion = ""
		if err := api.Create(ctx, &cm); err != nil {
			t.Fatal(err)
		}
	}
	var run operations.GuardedRun
	_ = seeded.Get(ctx, key, &run)
	run.ResourceVersion = ""
	run.UID = ""
	if err := api.Create(ctx, &run); err != nil {
		t.Fatal(err)
	}
	if run.Spec.Mode != "EvaluateOnly" {
		t.Fatalf("default mode=%q", run.Spec.Mode)
	}
	changed := run.DeepCopy()
	changed.Spec.Mode = "Execute"
	if err := api.Update(ctx, changed); !apierrors.IsInvalid(err) {
		t.Fatalf("immutable spec accepted: %v", err)
	}
	invalid := run.DeepCopy()
	invalid.ResourceVersion = ""
	invalid.UID = ""
	invalid.Name = "invalid"
	invalid.Spec.Mode = "unknown"
	if err := api.Create(ctx, invalid); !apierrors.IsInvalid(err) {
		t.Fatalf("invalid mode accepted: %v", err)
	}
	// The real status subresource must reject a decision based on an old resource version.
	original := run.DeepCopy()
	run.Status.JobName, run.Status.JobUID = "recorded-job", "recorded-uid"
	if err := api.Status().Update(ctx, &run); err != nil {
		t.Fatal(err)
	}
	direct := &GuardedRunReconciler{Client: api}
	if _, err := direct.report(ctx, original.DeepCopy(), original, "Blocked", "PolicyDenied", "Stale decision", false); !apierrors.IsConflict(err) {
		t.Fatalf("stale API status patch accepted: %v", err)
	}
	if err := api.Get(ctx, key, &run); err != nil || run.Status.JobUID != "recorded-uid" {
		t.Fatalf("recorded execution identity lost: status=%+v error=%v", run.Status, err)
	}
	run.Status = operations.GuardedRunStatus{}
	if err := api.Status().Update(ctx, &run); err != nil {
		t.Fatal(err)
	}
	manager, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: seeded.Scheme, Metrics: metricsserver.Options{BindAddress: "0"}, HealthProbeBindAddress: "0", Cache: cache.Options{DefaultNamespaces: map[string]cache.Config{key.Namespace: {}}}})
	if err != nil {
		t.Fatal(err)
	}
	reconciler := &GuardedRunReconciler{Client: manager.GetClient(), APIReader: manager.GetAPIReader(), Scheme: seeded.Scheme, Policy: seeded.Policy, Now: seeded.Now}
	if err := reconciler.SetupWithManager(manager); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- manager.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-stopped; err != nil {
			t.Error(err)
		}
	})
	eventually := func(check func() bool) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if check() {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("controller did not converge")
	}
	eventually(func() bool { _ = api.Get(ctx, key, &run); return run.Status.Phase == "Permitted" })
	var jobs batchv1.JobList
	_ = api.List(ctx, &jobs)
	if len(jobs.Items) != 0 {
		t.Fatal("evaluation launched a Job")
	}
	execute := run.DeepCopy()
	execute.Name = "execute"
	execute.UID = ""
	execute.ResourceVersion = ""
	execute.Status = operations.GuardedRunStatus{}
	execute.Spec.Mode = "Execute"
	if err := api.Create(ctx, execute); err != nil {
		t.Fatal(err)
	}
	executeKey := client.ObjectKeyFromObject(execute)
	eventually(func() bool { _ = api.Get(ctx, executeKey, execute); return execute.Status.Phase == "Running" })
	// API-backed reconciliation after a new controller instance must retain the execution.
	restarted := &GuardedRunReconciler{Client: api, Scheme: seeded.Scheme, Policy: seeded.Policy, Now: seeded.Now}
	if _, err := restarted.Reconcile(ctx, ctrl.Request{NamespacedName: executeKey}); err != nil {
		t.Fatal(err)
	}
	_ = api.List(ctx, &jobs)
	if len(jobs.Items) != 1 || !metav1.IsControlledBy(&jobs.Items[0], execute) {
		t.Fatalf("unexpected Jobs: %+v", jobs.Items)
	}
}
