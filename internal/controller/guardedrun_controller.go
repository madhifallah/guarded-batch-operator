// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	operations "github.com/madhifallah/guarded-batch-operator/api/v1alpha1"
)

type PolicyEvaluator interface {
	Allow(context.Context, string, map[string]any) (bool, error)
}

// GuardedRunReconciler launches administrator-defined Jobs after checking evidence.
type GuardedRunReconciler struct {
	client.Client
	// APIReader bypasses the informer cache for execution identity and status decisions.
	APIReader client.Reader
	Scheme    *runtime.Scheme
	Policy    PolicyEvaluator
	Recorder  record.EventRecorder
	Now       func() time.Time
}

// +kubebuilder:rbac:groups=operations.example.org,resources=guardedruns,verbs=get;list;watch,namespace=system
// +kubebuilder:rbac:groups=operations.example.org,resources=guardedruns/status,verbs=get;update;patch,namespace=system
// +kubebuilder:rbac:groups=operations.example.org,resources=guardedruns/finalizers,verbs=update,namespace=system
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create,namespace=system
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch,namespace=system
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch,namespace=system

func (r *GuardedRunReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	var run operations.GuardedRun
	if err := reader.Get(ctx, req.NamespacedName, &run); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	original := run.DeepCopy()
	report := func(phase, reason, message string, allowed bool) (ctrl.Result, error) {
		return r.report(ctx, &run, original, phase, reason, message, allowed)
	}
	if !run.DeletionTimestamp.IsZero() || run.Status.Phase == "Succeeded" || run.Status.Phase == "Failed" {
		return ctrl.Result{}, nil
	}
	if run.UID == "" {
		return ctrl.Result{}, fmt.Errorf("request UID is missing")
	}
	name := "guardedrun-" + string(run.UID)
	var job batchv1.Job
	err := reader.Get(ctx, client.ObjectKey{Namespace: run.Namespace, Name: name}, &job)
	if err == nil {
		if run.Status.JobUID != "" && run.Status.JobUID != string(job.UID) {
			return report("Failed", "JobReplaced", "The execution Job identity changed", false)
		}
		if !metav1.IsControlledBy(&job, &run) {
			return report("Blocked", "NameConflict", "A foreign Job occupies the execution name", false)
		}
		run.Status.JobName, run.Status.JobUID = job.Name, string(job.UID)
		for _, c := range job.Status.Conditions {
			if c.Status != corev1.ConditionTrue {
				continue
			}
			if c.Type == batchv1.JobComplete {
				return report("Succeeded", "JobComplete", "Execution completed", true)
			}
			if c.Type == batchv1.JobFailed {
				return report("Failed", "JobFailed", "Execution failed; submit a new request to retry", false)
			}
		}
		return report("Running", "JobRunning", "Execution Job exists", true)
	}
	if !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	if run.Status.JobName != "" {
		return report("Failed", "JobMissing", "The observed execution Job disappeared; it will not be recreated", false)
	}
	if run.Spec.Mode != "" && run.Spec.Mode != "EvaluateOnly" && run.Spec.Mode != "Execute" {
		return report("Blocked", "InvalidMode", "Unknown execution mode", false)
	}
	configs := make([]corev1.ConfigMap, 3)
	for i, ref := range []string{run.Spec.TemplateRef, run.Spec.PolicyRef, run.Spec.EvidenceRef} {
		if err := r.Get(ctx, client.ObjectKey{Namespace: run.Namespace, Name: ref}, &configs[i]); err != nil {
			if apierrors.IsNotFound(err) {
				return report("Blocked", "ConfigurationMissing", "A referenced ConfigMap is unavailable", false)
			}
			return ctrl.Result{}, err
		}
	}
	var spec batchv1.JobSpec
	if err := decodeJSON(configs[0].Data["job.json"], &spec); err != nil {
		return report("Blocked", "InvalidTemplate", "job.json must contain a single valid JobSpec", false)
	}
	if err := validateTemplate(spec); err != nil {
		return report("Blocked", "InvalidTemplate", err.Error(), false)
	}
	var facts map[string]any
	if err := decodeJSON(configs[2].Data["evidence.json"], &facts); err != nil || facts == nil {
		return report("Blocked", "InvalidEvidence", "evidence.json must contain a single object", false)
	}
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	evalCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	allowed, err := r.Policy.Allow(evalCtx, configs[1].Data["policy.rego"], map[string]any{"facts": facts, "now_unix": now.Unix(), "job": spec})
	if err != nil {
		return report("Blocked", "PolicyError", "Policy compilation or evaluation failed", false)
	}
	if !allowed {
		return report("Blocked", "PolicyDenied", "Operational prerequisites are not satisfied", false)
	}
	if run.Spec.Mode != "Execute" {
		return report("Permitted", "EvaluationOnly", "Policy permits execution; evaluation mode creates no Job", true)
	}
	job = batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: run.Namespace}, Spec: spec}
	if err := ctrl.SetControllerReference(&run, &job, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.Create(ctx, &job); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		return ctrl.Result{}, err
	}
	run.Status.JobName, run.Status.JobUID = job.Name, string(job.UID)
	return report("Running", "JobCreated", "Execution Job created", true)
}

// decodeJSON preserves evidence integers and rejects trailing objects or garbage.
func decodeJSON(source string, target any) error {
	decoder := json.NewDecoder(strings.NewReader(source))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("expected end of JSON input")
	}
	return nil
}

func validateTemplate(spec batchv1.JobSpec) error {
	if spec.Suspend != nil && *spec.Suspend {
		return fmt.Errorf("suspended Jobs cannot enforce an execution deadline")
	}
	if spec.ManagedBy != nil && *spec.ManagedBy != batchv1.JobControllerName {
		return fmt.Errorf("Jobs must use the built-in Kubernetes Job controller")
	}
	if spec.ActiveDeadlineSeconds == nil || *spec.ActiveDeadlineSeconds < 1 || *spec.ActiveDeadlineSeconds > 3600 {
		return fmt.Errorf("activeDeadlineSeconds must be between 1 and 3600")
	}
	if spec.BackoffLimit == nil || *spec.BackoffLimit < 0 || *spec.BackoffLimit > 3 {
		return fmt.Errorf("backoffLimit must be between 0 and 3")
	}
	if spec.TTLSecondsAfterFinished != nil {
		return fmt.Errorf("automatic Job TTL is unsupported: keep Jobs until their outcome is observed")
	}
	if spec.Template.Spec.RestartPolicy != corev1.RestartPolicyNever {
		return fmt.Errorf("restartPolicy must be Never")
	}
	if len(spec.Template.Spec.Containers) == 0 {
		return fmt.Errorf("at least one container is required")
	}
	for _, c := range append(spec.Template.Spec.Containers, spec.Template.Spec.InitContainers...) {
		if c.Resources.Limits.Memory().Sign() <= 0 || c.Resources.Limits.Cpu().Sign() <= 0 {
			return fmt.Errorf("each container requires positive CPU and memory limits")
		}
	}
	return nil
}

func (r *GuardedRunReconciler) report(ctx context.Context, run, previous *operations.GuardedRun, phase, reason, message string, allowed bool) (ctrl.Result, error) {
	run.Status.Phase, run.Status.ObservedGeneration = phase, run.Generation
	status := metav1.ConditionFalse
	if allowed {
		status = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{Type: "Ready", Status: status, Reason: reason, Message: message, ObservedGeneration: run.Generation})
	if !reflect.DeepEqual(previous.Status, run.Status) {
		if err := r.Status().Patch(ctx, run, client.MergeFromWithOptions(previous, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, err
		}
		if r.Recorder != nil && (previous.Status.Phase != phase || len(previous.Status.Conditions) == 0 || previous.Status.Conditions[0].Reason != reason) {
			r.Recorder.Event(run, corev1.EventTypeNormal, reason, message)
		}
	}
	if phase == "Blocked" || phase == "Permitted" {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	return ctrl.Result{}, nil
}

func (r *GuardedRunReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&operations.GuardedRun{}).Owns(&batchv1.Job{}).
		Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
			var runs operations.GuardedRunList
			if err := r.List(ctx, &runs, client.InNamespace(obj.GetNamespace())); err != nil {
				return nil
			}
			var requests []reconcile.Request
			for _, run := range runs.Items {
				if run.Spec.TemplateRef == obj.GetName() || run.Spec.PolicyRef == obj.GetName() || run.Spec.EvidenceRef == obj.GetName() {
					requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&run)})
				}
			}
			return requests
		})).Named("guardedrun").Complete(r)
}
