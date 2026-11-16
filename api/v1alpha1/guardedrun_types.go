/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// GuardedRunSpec is an immutable execution request.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="execution requests are immutable"
type GuardedRunSpec struct {
	// TemplateRef names a trusted ConfigMap containing job.json (batch/v1 JobSpec).
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	TemplateRef string `json:"templateRef"`
	// PolicyRef names a trusted ConfigMap containing policy.rego.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	PolicyRef string `json:"policyRef"`
	// EvidenceRef names a trusted producer's ConfigMap containing evidence.json.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	EvidenceRef string `json:"evidenceRef"`
	// Mode selects evaluation only or Job creation. Submit a new request to change mode.
	// +kubebuilder:default=EvaluateOnly
	// +kubebuilder:validation:Enum=EvaluateOnly;Execute
	// +optional
	Mode string `json:"mode,omitempty"`
}

// GuardedRunStatus describes the decision and observed execution.
type GuardedRunStatus struct {
	// Phase is Blocked, Permitted, Running, Succeeded, or Failed.
	// +optional
	Phase string `json:"phase,omitempty"`
	// JobName is the stable name derived from the request UID.
	// +optional
	JobName string `json:"jobName,omitempty"`
	// JobUID identifies the observed execution; missing Jobs are not silently replaced.
	// +optional
	JobUID string `json:"jobUID,omitempty"`
	// ObservedGeneration identifies the request processed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Conditions explain outcomes without copying evidence.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Job",type=string,JSONPath=`.status.jobName`

// GuardedRun is the Schema for the guardedruns API
type GuardedRun struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of GuardedRun
	// +required
	Spec GuardedRunSpec `json:"spec"`

	// status defines the observed state of GuardedRun
	// +optional
	Status GuardedRunStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// GuardedRunList contains a list of GuardedRun
type GuardedRunList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GuardedRun `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &GuardedRun{}, &GuardedRunList{})
		return nil
	})
}
