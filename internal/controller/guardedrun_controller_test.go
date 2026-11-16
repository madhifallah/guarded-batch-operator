// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	operations "github.com/madhifallah/guarded-batch-operator/api/v1alpha1"
	"github.com/madhifallah/guarded-batch-operator/internal/policy"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

var testNow = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

func fixture(t *testing.T, mode string) (*GuardedRunReconciler, client.ObjectKey) {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = operations.AddToScheme(scheme)
	source, err := os.ReadFile("../../policies/maintenance.rego")
	if err != nil {
		t.Fatal(err)
	}
	template, err := os.ReadFile("../../config/samples/job.json")
	if err != nil {
		t.Fatal(err)
	}
	key := client.ObjectKey{Namespace: "test", Name: "example"}
	run := &operations.GuardedRun{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace, UID: types.UID("11111111-1111-1111-1111-111111111111"), Generation: 1}, Spec: operations.GuardedRunSpec{TemplateRef: "template", PolicyRef: "policy", EvidenceRef: "evidence", Mode: mode}}
	objects := []client.Object{run,
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "template", Namespace: "test"}, Data: map[string]string{"job.json": string(template)}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "policy", Namespace: "test"}, Data: map[string]string{"policy.rego": string(source)}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "evidence", Namespace: "test"}, Data: map[string]string{"evidence.json": `{"backup":{"succeeded":true,"completedAt":"2026-11-16T11:00:00Z"},"maintenanceFreeze":false}`}},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&operations.GuardedRun{}, &batchv1.Job{}).WithObjects(objects...).Build()
	return &GuardedRunReconciler{Client: c, Scheme: scheme, Policy: &policy.Engine{}, Now: func() time.Time { return testNow }}, key
}

func reconcileRun(t *testing.T, r *GuardedRunReconciler, key client.ObjectKey) operations.GuardedRun {
	t.Helper()
	if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	var run operations.GuardedRun
	if err := r.Get(t.Context(), key, &run); err != nil {
		t.Fatal(err)
	}
	return run
}

func setConfig(t *testing.T, r *GuardedRunReconciler, name, key, value string) {
	t.Helper()
	var cm corev1.ConfigMap
	if err := r.Get(t.Context(), client.ObjectKey{Namespace: "test", Name: name}, &cm); err != nil {
		t.Fatal(err)
	}
	cm.Data[key] = value
	if err := r.Update(t.Context(), &cm); err != nil {
		t.Fatal(err)
	}
}

func TestEvaluationCreatesNoJob(t *testing.T) {
	r, key := fixture(t, "EvaluateOnly")
	run := reconcileRun(t, r, key)
	if run.Status.Phase != "Permitted" {
		t.Fatalf("status=%+v", run.Status)
	}
	var jobs batchv1.JobList
	_ = r.List(t.Context(), &jobs)
	if len(jobs.Items) != 0 {
		t.Fatal("evaluation created a Job")
	}
}

func TestBlockedRunReevaluatesWhenEvidenceChanges(t *testing.T) {
	r, key := fixture(t, "Execute")
	setConfig(t, r, "evidence", "evidence.json", `{"backup":{"succeeded":true,"completedAt":"2026-01-01T00:00:00Z"},"maintenanceFreeze":false}`)
	if run := reconcileRun(t, r, key); run.Status.Phase != "Blocked" || run.Status.Conditions[0].Reason != "PolicyDenied" {
		t.Fatalf("status=%+v", run.Status)
	}
	setConfig(t, r, "evidence", "evidence.json", `{"backup":{"succeeded":true,"completedAt":"2026-11-16T11:00:00Z"},"maintenanceFreeze":false}`)
	if run := reconcileRun(t, r, key); run.Status.Phase != "Running" {
		t.Fatalf("status=%+v", run.Status)
	}
}

func TestRestartAndRepeatedReconciliationKeepOneJob(t *testing.T) {
	r, key := fixture(t, "Execute")
	first := reconcileRun(t, r, key)
	restarted := &GuardedRunReconciler{Client: r.Client, Scheme: r.Scheme, Policy: &policy.Engine{}}
	for range 4 {
		reconcileRun(t, restarted, key)
	}
	var jobs batchv1.JobList
	_ = r.List(t.Context(), &jobs)
	if len(jobs.Items) != 1 || jobs.Items[0].Name != first.Status.JobName || !metav1.IsControlledBy(&jobs.Items[0], &first) {
		t.Fatalf("jobs=%+v", jobs.Items)
	}
}

func TestOutcomeAndMissingJobNeverLaunchAgain(t *testing.T) {
	for _, outcome := range []string{"complete", "failure", "missing"} {
		t.Run(outcome, func(t *testing.T) {
			r, key := fixture(t, "Execute")
			run := reconcileRun(t, r, key)
			var job batchv1.Job
			_ = r.Get(t.Context(), client.ObjectKey{Namespace: key.Namespace, Name: run.Status.JobName}, &job)
			want := "Failed"
			if outcome == "missing" {
				if err := r.Delete(t.Context(), &job); err != nil {
					t.Fatal(err)
				}
			} else {
				kind := batchv1.JobFailed
				if outcome == "complete" {
					kind = batchv1.JobComplete
					want = "Succeeded"
				}
				job.Status.Conditions = []batchv1.JobCondition{{Type: kind, Status: corev1.ConditionTrue}}
				if err := r.Status().Update(t.Context(), &job); err != nil {
					t.Fatal(err)
				}
			}
			if run = reconcileRun(t, r, key); run.Status.Phase != want {
				t.Fatalf("status=%+v", run.Status)
			}
			reconcileRun(t, r, key)
			var jobs batchv1.JobList
			_ = r.List(t.Context(), &jobs)
			if outcome == "missing" && len(jobs.Items) != 0 {
				t.Fatal("deleted execution was relaunched")
			}
		})
	}
}

func TestInvalidInputsFailClosed(t *testing.T) {
	for _, tc := range []struct{ name, ref, key, value, reason string }{
		{"malformed evidence", "evidence", "evidence.json", "{", "InvalidEvidence"},
		{"malformed policy", "policy", "policy.rego", "broken", "PolicyError"},
		{"malformed template", "template", "job.json", "{}", "InvalidTemplate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, key := fixture(t, "Execute")
			setConfig(t, r, tc.ref, tc.key, tc.value)
			run := reconcileRun(t, r, key)
			if run.Status.Phase != "Blocked" || run.Status.Conditions[0].Reason != tc.reason {
				t.Fatalf("status=%+v", run.Status)
			}
			var jobs batchv1.JobList
			_ = r.List(t.Context(), &jobs)
			if len(jobs.Items) != 0 {
				t.Fatal("invalid configuration launched a Job")
			}
		})
	}
}

func TestForeignJobIsNeverAdopted(t *testing.T) {
	r, key := fixture(t, "Execute")
	var run operations.GuardedRun
	_ = r.Get(t.Context(), key, &run)
	foreign := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "guardedrun-" + string(run.UID), Namespace: key.Namespace}}
	if err := r.Create(t.Context(), foreign); err != nil {
		t.Fatal(err)
	}
	run = reconcileRun(t, r, key)
	if run.Status.Conditions[0].Reason != "NameConflict" {
		t.Fatalf("status=%+v", run.Status)
	}
}

func TestTemplateBounds(t *testing.T) {
	raw, err := os.ReadFile("../../config/samples/job.json")
	if err != nil {
		t.Fatal(err)
	}
	t.Run("explicit native controller", func(t *testing.T) {
		var spec batchv1.JobSpec
		if err := json.Unmarshal(raw, &spec); err != nil {
			t.Fatal(err)
		}
		suspended, manager := false, batchv1.JobControllerName
		spec.Suspend, spec.ManagedBy = &suspended, &manager
		if err := validateTemplate(spec); err != nil {
			t.Fatalf("valid native template rejected: %v", err)
		}
	})
	for _, tc := range []struct {
		name string
		edit func(*batchv1.JobSpec)
	}{
		{"no deadline", func(s *batchv1.JobSpec) { s.ActiveDeadlineSeconds = nil }},
		{"no retry bound", func(s *batchv1.JobSpec) { s.BackoffLimit = nil }},
		{"too many retries", func(s *batchv1.JobSpec) { v := int32(4); s.BackoffLimit = &v }},
		{"ttl", func(s *batchv1.JobSpec) { v := int32(1); s.TTLSecondsAfterFinished = &v }},
		{"no resources", func(s *batchv1.JobSpec) { s.Template.Spec.Containers[0].Resources = corev1.ResourceRequirements{} }},
		{"restart", func(s *batchv1.JobSpec) { s.Template.Spec.RestartPolicy = corev1.RestartPolicyOnFailure }},
		{"suspended", func(s *batchv1.JobSpec) { v := true; s.Suspend = &v }},
		{"external controller", func(s *batchv1.JobSpec) { v := "example.org/external"; s.ManagedBy = &v }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var spec batchv1.JobSpec
			_ = json.Unmarshal(raw, &spec)
			tc.edit(&spec)
			if validateTemplate(spec) == nil {
				t.Fatal("invalid template accepted")
			}
		})
	}
}

func TestLaggingJobCacheDoesNotFailExistingExecution(t *testing.T) {
	r, key := fixture(t, "Execute")
	first := reconcileRun(t, r, key)
	authority := r.Client
	r.APIReader = authority
	r.Client = interceptor.NewClient(authority.(client.WithWatch), interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*batchv1.Job); ok {
				return apierrors.NewNotFound(schema.GroupResource{Group: "batch", Resource: "jobs"}, key.Name)
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})
	run := reconcileRun(t, r, key)
	if run.Status.Phase != "Running" || run.Status.JobName != first.Status.JobName {
		t.Fatalf("cache lag changed execution: %+v", run.Status)
	}
}

func TestLaggingRunCacheCannotRelaunchObservedExecution(t *testing.T) {
	r, key := fixture(t, "Execute")
	var stale operations.GuardedRun
	if err := r.Get(t.Context(), key, &stale); err != nil {
		t.Fatal(err)
	}
	first := reconcileRun(t, r, key)
	if err := r.Delete(t.Context(), &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: first.Status.JobName, Namespace: key.Namespace}}); err != nil {
		t.Fatal(err)
	}
	authority := r.Client
	r.APIReader = authority
	r.Client = interceptor.NewClient(authority.(client.WithWatch), interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if target, ok := obj.(*operations.GuardedRun); ok {
				stale.DeepCopyInto(target)
				return nil
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})
	for range 2 {
		if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
	}
	var actual operations.GuardedRun
	if err := authority.Get(t.Context(), key, &actual); err != nil {
		t.Fatal(err)
	}
	var jobs batchv1.JobList
	if err := authority.List(t.Context(), &jobs); err != nil {
		t.Fatal(err)
	}
	if actual.Status.Phase != "Failed" || actual.Status.Conditions[0].Reason != "JobMissing" || len(jobs.Items) != 0 {
		t.Fatalf("observed execution relaunched: status=%+v jobs=%d", actual.Status, len(jobs.Items))
	}
}

func TestStatusConflictCannotOverwriteExecutionIdentity(t *testing.T) {
	r, key := fixture(t, "Execute")
	var original operations.GuardedRun
	if err := r.Get(t.Context(), key, &original); err != nil {
		t.Fatal(err)
	}
	current := original.DeepCopy()
	current.Status.JobName, current.Status.JobUID = "observed-job", "observed-uid"
	if err := r.Status().Update(t.Context(), current); err != nil {
		t.Fatal(err)
	}
	_, err := r.report(t.Context(), original.DeepCopy(), &original, "Blocked", "PolicyDenied", "Prerequisites denied", false)
	if !apierrors.IsConflict(err) {
		t.Fatalf("expected stale decision conflict, got %v", err)
	}
	if err := r.Get(t.Context(), key, current); err != nil {
		t.Fatal(err)
	}
	if current.Status.JobUID != "observed-uid" {
		t.Fatal("stale status erased recorded execution")
	}
}

func TestStatusWriteFailureRecoversExistingJob(t *testing.T) {
	r, key := fixture(t, "Execute")
	authority := r.Client
	creates := 0
	r.APIReader = authority
	r.Client = interceptor.NewClient(authority.(client.WithWatch), interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*batchv1.Job); ok {
				creates++
				obj.SetUID("created-job")
			}
			return c.Create(ctx, obj, opts...)
		},
		SubResourcePatch: func(context.Context, client.Client, string, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
			return errors.New("synthetic status-write failure")
		},
	})
	if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: key}); err == nil {
		t.Fatal("status-write failure was hidden")
	}
	r.Client = authority
	run := reconcileRun(t, r, key)
	if creates != 1 || run.Status.Phase != "Running" || run.Status.JobUID != "created-job" {
		t.Fatalf("failed status write did not recover: creates=%d status=%+v", creates, run.Status)
	}
}

func TestReplacedJobIsNotAdopted(t *testing.T) {
	for _, ownership := range []string{"same owner", "foreign"} {
		t.Run(ownership, func(t *testing.T) {
			r, key := fixture(t, "Execute")
			first := reconcileRun(t, r, key)
			var job batchv1.Job
			jobKey := client.ObjectKey{Namespace: key.Namespace, Name: first.Status.JobName}
			if err := r.Get(t.Context(), jobKey, &job); err != nil {
				t.Fatal(err)
			}
			if err := r.Delete(t.Context(), &job); err != nil {
				t.Fatal(err)
			}
			// Simulate distinct API-assigned identities, with the same owner reference.
			var run operations.GuardedRun
			if err := r.Get(t.Context(), key, &run); err != nil {
				t.Fatal(err)
			}
			run.Status.JobUID = "original-job"
			if err := r.Status().Update(t.Context(), &run); err != nil {
				t.Fatal(err)
			}
			if ownership == "foreign" {
				job.OwnerReferences = nil
			}
			job.ResourceVersion, job.UID = "", "replacement-job"
			if err := r.Create(t.Context(), &job); err != nil {
				t.Fatal(err)
			}
			run = reconcileRun(t, r, key)
			if run.Status.Phase != "Failed" || run.Status.Conditions[0].Reason != "JobReplaced" || run.Status.JobUID != "original-job" {
				t.Fatalf("replacement adopted: %+v", run.Status)
			}
		})
	}
}

func TestJSONInputsRejectTrailingData(t *testing.T) {
	for _, ref := range []string{"template", "evidence"} {
		for _, suffix := range []string{" {}", " garbage"} {
			t.Run(ref+suffix, func(t *testing.T) {
				r, key := fixture(t, "Execute")
				var cm corev1.ConfigMap
				if err := r.Get(t.Context(), client.ObjectKey{Namespace: key.Namespace, Name: ref}, &cm); err != nil {
					t.Fatal(err)
				}
				file := "job.json"
				if ref == "evidence" {
					file = "evidence.json"
				}
				setConfig(t, r, ref, file, cm.Data[file]+suffix)
				run := reconcileRun(t, r, key)
				var jobs batchv1.JobList
				if err := r.List(t.Context(), &jobs); err != nil {
					t.Fatal(err)
				}
				if run.Status.Phase != "Blocked" || len(jobs.Items) != 0 {
					t.Fatalf("trailing JSON executed: %+v", run.Status)
				}
			})
		}
	}
}

func TestEvidenceIntegersRetainPolicyPrecision(t *testing.T) {
	r, key := fixture(t, "Execute")
	setConfig(t, r, "evidence", "evidence.json", `{"revision":9007199254740993}`)
	setConfig(t, r, "policy", "policy.rego", "package guarded\nallow := input.facts.revision == 9007199254740992")
	if run := reconcileRun(t, r, key); run.Status.Phase != "Blocked" {
		t.Fatalf("rounded evidence authorized: %+v", run.Status)
	}
	setConfig(t, r, "policy", "policy.rego", "package guarded\nallow := input.facts.revision == 9007199254740993")
	if run := reconcileRun(t, r, key); run.Status.Phase != "Running" {
		t.Fatalf("exact evidence value lost: %+v", run.Status)
	}
}
