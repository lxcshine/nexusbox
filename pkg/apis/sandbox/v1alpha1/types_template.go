/*
Copyright 2024 NexusBox Authors.

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
)

// SandboxTemplateSpec defines the desired state of a SandboxTemplate.
type SandboxTemplateSpec struct {
	// Runtime specifies the default isolation runtime type.
	Runtime SandboxRuntimeType `json:"runtime" protobuf:"bytes,1,opt,name=runtime"`

	// Priority determines default scheduling priority.
	Priority SandboxPriority `json:"priority" protobuf:"varint,2,opt,name=priority"`

	// SchedulingPolicy defines default scheduling policy.
	SchedulingPolicy SandboxSchedulingPolicy `json:"schedulingPolicy" protobuf:"bytes,3,opt,name=schedulingPolicy"`

	// Resources defines the default resource requirements.
	Resources ResourceRequirements `json:"resources" protobuf:"bytes,4,opt,name=resources"`

	// Image specifies the default container image.
	Image string `json:"image" protobuf:"bytes,5,opt,name=image"`

	// Command specifies the default command.
	// +optional
	Command []string `json:"command,omitempty" protobuf:"bytes,6,rep,name=command"`

	// Args specifies the default arguments.
	// +optional
	Args []string `json:"args,omitempty" protobuf:"bytes,7,rep,name=args"`

	// Env specifies default environment variables.
	// +optional
	Env []EnvVar `json:"env,omitempty" protobuf:"bytes,8,rep,name=env"`

	// WorkingDir specifies the default working directory.
	// +optional
	WorkingDir string `json:"workingDir,omitempty" protobuf:"bytes,9,opt,name=workingDir"`

	// NodeSelector specifies default node selector constraints.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty" protobuf:"bytes,10,rep,name=nodeSelector"`

	// Tolerations specifies default tolerations.
	// +optional
	Tolerations []Toleration `json:"tolerations,omitempty" protobuf:"bytes,11,rep,name=tolerations"`

	// MaxLifetimeSeconds specifies the default maximum lifetime.
	// +optional
	MaxLifetimeSeconds *int64 `json:"maxLifetimeSeconds,omitempty" protobuf:"varint,12,opt,name=maxLifetimeSeconds"`

	// IdleTimeoutSeconds specifies the default idle timeout.
	// +optional
	IdleTimeoutSeconds *int64 `json:"idleTimeoutSeconds,omitempty" protobuf:"varint,13,opt,name=idleTimeoutSeconds"`

	// RestartPolicy defines the default restart policy.
	RestartPolicy RestartPolicy `json:"restartPolicy" protobuf:"bytes,14,opt,name=restartPolicy"`

	// Network specifies the default network configuration.
	// +optional
	Network *SandboxNetworkSpec `json:"network,omitempty" protobuf:"bytes,15,opt,name=network"`

	// Storage specifies the default storage configuration.
	// +optional
	Storage *SandboxStorageSpec `json:"storage,omitempty" protobuf:"bytes,16,opt,name=storage"`

	// Security specifies the default security configuration.
	// +optional
	Security *SandboxSecuritySpec `json:"security,omitempty" protobuf:"bytes,17,opt,name=security"`

	// AllowedOverrides specifies which fields can be overridden by the Sandbox.
	AllowedOverrides []string `json:"allowedOverrides,omitempty" protobuf:"bytes,18,rep,name=allowedOverrides"`
}

// SandboxTemplateStatus defines the observed state of a SandboxTemplate.
type SandboxTemplateStatus struct {
	// UsageCount is the number of sandboxes created from this template.
	UsageCount int64 `json:"usageCount,omitempty" protobuf:"varint,1,opt,name=usageCount"`

	// LastUsedTime is the last time this template was used.
	// +optional
	LastUsedTime *metav1.Time `json:"lastUsedTime,omitempty" protobuf:"bytes,2,opt,name=lastUsedTime"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=sbt
// +kubebuilder:printcolumn:name="Runtime",type=string,JSONPath=`.spec.runtime`
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image`
// +kubebuilder:printcolumn:name="Usage",type=integer,JSONPath=`.status.usageCount`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// SandboxTemplate is the Schema for the sandboxtemplates API.
type SandboxTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SandboxTemplateSpec   `json:"spec,omitempty"`
	Status SandboxTemplateStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// SandboxTemplateList contains a list of SandboxTemplate.
type SandboxTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []SandboxTemplate `json:"items"`
}
