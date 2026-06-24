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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TenantPhase represents the current phase of a tenant.
type TenantPhase string

const (
	// TenantPending indicates the tenant is pending initialization.
	TenantPending TenantPhase = "Pending"
	// TenantActive indicates the tenant is active and can create sandboxes.
	TenantActive TenantPhase = "Active"
	// TenantSuspended indicates the tenant is suspended.
	TenantSuspended TenantPhase = "Suspended"
	// TenantTerminating indicates the tenant is being deleted.
	TenantTerminating TenantPhase = "Terminating"
)

// IsAvailable returns true if the tenant can create new sandboxes.
func (p TenantPhase) IsAvailable() bool {
	return p == TenantActive
}

// TenantSpec defines the desired state of a Tenant.
type TenantSpec struct {
	// DisplayName is the human-readable name of the tenant.
	DisplayName string `json:"displayName" protobuf:"bytes,1,opt,name=displayName"`

	// Description describes the tenant.
	// +optional
	Description string `json:"description,omitempty" protobuf:"bytes,2,opt,name=description"`

	// AdminContacts is a list of admin contact information.
	// +optional
	AdminContacts []ContactInfo `json:"adminContacts,omitempty" protobuf:"bytes,3,rep,name=adminContacts"`

	// ResourceQuota defines the resource quota for this tenant.
	ResourceQuota TenantResourceQuota `json:"resourceQuota" protobuf:"bytes,4,opt,name=resourceQuota"`

	// IsolationLevel defines the isolation level for this tenant's sandboxes.
	IsolationLevel TenantIsolationLevel `json:"isolationLevel" protobuf:"bytes,5,opt,name=isolationLevel"`

	// AllowedRuntimes specifies which runtimes this tenant is allowed to use.
	AllowedRuntimes []SandboxRuntimeType `json:"allowedRuntimes" protobuf:"bytes,6,rep,name=allowedRuntimes"`

	// AllowedSchedulingPolicies specifies which scheduling policies this tenant can use.
	AllowedSchedulingPolicies []SandboxSchedulingPolicy `json:"allowedSchedulingPolicies" protobuf:"bytes,7,rep,name=allowedSchedulingPolicies"`

	// PriorityClass defines the default priority for this tenant's sandboxes.
	PriorityClass SandboxPriority `json:"priorityClass" protobuf:"varint,8,opt,name=priorityClass"`

	// MaxConcurrentSandboxes is the maximum number of concurrent sandboxes.
	MaxConcurrentSandboxes int32 `json:"maxConcurrentSandboxes" protobuf:"varint,9,opt,name=maxConcurrentSandboxes"`

	// MaxSandboxesPerDay is the maximum number of sandboxes that can be created per day.
	// +optional
	MaxSandboxesPerDay int32 `json:"maxSandboxesPerDay,omitempty" protobuf:"varint,10,opt,name=maxSandboxesPerDay"`

	// DefaultSandboxSpec contains default values for sandbox specs created by this tenant.
	// +optional
	DefaultSandboxSpec *DefaultSandboxSpec `json:"defaultSandboxSpec,omitempty" protobuf:"bytes,11,opt,name=defaultSandboxSpec"`

	// NetworkPolicy defines the network isolation policy for this tenant.
	// +optional
	NetworkPolicy *TenantNetworkPolicy `json:"networkPolicy,omitempty" protobuf:"bytes,12,opt,name=networkPolicy"`

	// CostCenter identifies the cost center for billing.
	// +optional
	CostCenter string `json:"costCenter,omitempty" protobuf:"bytes,13,opt,name=costCenter"`

	// Labels are custom labels for the tenant.
	// +optional
	Labels map[string]string `json:"labels,omitempty" protobuf:"bytes,14,rep,name=labels"`

	// AuditLogging indicates whether audit logging is enabled.
	// +optional
	AuditLogging bool `json:"auditLogging,omitempty" protobuf:"varint,15,opt,name=auditLogging"`

	// RateLimit defines rate limiting configuration.
	// +optional
	RateLimit *RateLimitConfig `json:"rateLimit,omitempty" protobuf:"bytes,16,opt,name=rateLimit"`
}

// ContactInfo contains contact information.
type ContactInfo struct {
	// Name of the contact person.
	Name string `json:"name" protobuf:"bytes,1,opt,name=name"`
	// Email of the contact person.
	Email string `json:"email" protobuf:"bytes,2,opt,name=email"`
	// Phone of the contact person.
	// +optional
	Phone string `json:"phone,omitempty" protobuf:"bytes,3,opt,name=phone"`
	// Role of the contact person.
	// +optional
	Role string `json:"role,omitempty" protobuf:"bytes,4,opt,name=role"`
}

// TenantIsolationLevel defines the isolation level for a tenant.
type TenantIsolationLevel string

const (
	// IsolationLevelStandard provides standard multi-tenant isolation.
	IsolationLevelStandard TenantIsolationLevel = "Standard"
	// IsolationLevelEnhanced provides enhanced isolation with dedicated resources.
	IsolationLevelEnhanced TenantIsolationLevel = "Enhanced"
	// IsolationLevelMaximum provides maximum isolation with dedicated nodes.
	IsolationLevelMaximum TenantIsolationLevel = "Maximum"
)

// TenantResourceQuota defines the resource quota for a tenant.
type TenantResourceQuota struct {
	// CPU is the total CPU quota for the tenant.
	CPU string `json:"cpu" protobuf:"bytes,1,opt,name=cpu"`
	// Memory is the total memory quota for the tenant.
	Memory string `json:"memory" protobuf:"bytes,2,opt,name=memory"`
	// GPU is the total GPU quota for the tenant.
	// +optional
	GPU string `json:"gpu,omitempty" protobuf:"bytes,3,opt,name=gpu"`
	// EphemeralStorage is the total ephemeral storage quota.
	// +optional
	EphemeralStorage string `json:"ephemeralStorage,omitempty" protobuf:"bytes,4,opt,name=ephemeralStorage"`
	// PersistentStorage is the total persistent storage quota.
	// +optional
	PersistentStorage string `json:"persistentStorage,omitempty" protobuf:"bytes,5,opt,name=persistentStorage"`
	// MaxInstances is the maximum number of sandbox instances.
	MaxInstances int32 `json:"maxInstances" protobuf:"varint,6,opt,name=maxInstances"`
	// MaxInstancesPerNode is the maximum number of sandbox instances per node.
	// +optional
	MaxInstancesPerNode int32 `json:"maxInstancesPerNode,omitempty" protobuf:"varint,7,opt,name=maxInstancesPerNode"`
	// ReservedResources defines resources reserved exclusively for this tenant.
	// +optional
	ReservedResources *ReservedResources `json:"reservedResources,omitempty" protobuf:"bytes,8,opt,name=reservedResources"`
}

// ReservedResources defines resources reserved for a tenant.
type ReservedResources struct {
	// CPU is the reserved CPU.
	CPU string `json:"cpu" protobuf:"bytes,1,opt,name=cpu"`
	// Memory is the reserved memory.
	Memory string `json:"memory" protobuf:"bytes,2,opt,name=memory"`
	// GPU is the reserved GPU.
	// +optional
	GPU string `json:"gpu,omitempty" protobuf:"bytes,3,opt,name=gpu"`
	// NodeSelector specifies nodes where reserved resources are located.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty" protobuf:"bytes,4,rep,name=nodeSelector"`
}

// DefaultSandboxSpec contains default values for sandbox specs.
type DefaultSandboxSpec struct {
	// Runtime is the default runtime type.
	// +optional
	Runtime *SandboxRuntimeType `json:"runtime,omitempty" protobuf:"bytes,1,opt,name=runtime"`
	// Priority is the default priority.
	// +optional
	Priority *SandboxPriority `json:"priority,omitempty" protobuf:"varint,2,opt,name=priority"`
	// SchedulingPolicy is the default scheduling policy.
	// +optional
	SchedulingPolicy *SandboxSchedulingPolicy `json:"schedulingPolicy,omitempty" protobuf:"bytes,3,opt,name=schedulingPolicy"`
	// Resources is the default resource requirements.
	// +optional
	Resources *ResourceRequirements `json:"resources,omitempty" protobuf:"bytes,4,opt,name=resources"`
	// MaxLifetimeSeconds is the default max lifetime.
	// +optional
	MaxLifetimeSeconds *int64 `json:"maxLifetimeSeconds,omitempty" protobuf:"varint,5,opt,name=maxLifetimeSeconds"`
	// IdleTimeoutSeconds is the default idle timeout.
	// +optional
	IdleTimeoutSeconds *int64 `json:"idleTimeoutSeconds,omitempty" protobuf:"varint,6,opt,name=idleTimeoutSeconds"`
	// RestartPolicy is the default restart policy.
	// +optional
	RestartPolicy *RestartPolicy `json:"restartPolicy,omitempty" protobuf:"bytes,7,opt,name=restartPolicy"`
	// Security is the default security configuration.
	// +optional
	Security *SandboxSecuritySpec `json:"security,omitempty" protobuf:"bytes,8,opt,name=security"`
	// Network is the default network configuration.
	// +optional
	Network *SandboxNetworkSpec `json:"network,omitempty" protobuf:"bytes,9,opt,name=network"`
}

// TenantNetworkPolicy defines the network policy for a tenant.
type TenantNetworkPolicy struct {
	// AllowInterTenantCommunication indicates whether sandboxes can communicate
	// with sandboxes from other tenants.
	AllowInterTenantCommunication bool `json:"allowInterTenantCommunication,omitempty" protobuf:"varint,1,opt,name=allowInterTenantCommunication"`
	// AllowedEgressCIDRs specifies CIDRs that sandboxes can connect to.
	// +optional
	AllowedEgressCIDRs []string `json:"allowedEgressCIDRs,omitempty" protobuf:"bytes,2,rep,name=allowedEgressCIDRs"`
	// DeniedEgressCIDRs specifies CIDRs that sandboxes cannot connect to.
	// +optional
	DeniedEgressCIDRs []string `json:"deniedEgressCIDRs,omitempty" protobuf:"bytes,3,rep,name=deniedEgressCIDRs"`
	// AllowedIngressFromTenants specifies tenants that can send traffic.
	// +optional
	AllowedIngressFromTenants []string `json:"allowedIngressFromTenants,omitempty" protobuf:"bytes,4,rep,name=allowedIngressFromTenants"`
	// BandwidthLimitMbps specifies the bandwidth limit per sandbox.
	// +optional
	BandwidthLimitMbps int32 `json:"bandwidthLimitMbps,omitempty" protobuf:"varint,5,opt,name=bandwidthLimitMbps"`
	// EnableNetworkLogging indicates whether network logging is enabled.
	// +optional
	EnableNetworkLogging bool `json:"enableNetworkLogging,omitempty" protobuf:"varint,6,opt,name=enableNetworkLogging"`
}

// RateLimitConfig defines rate limiting configuration.
type RateLimitConfig struct {
	// SandboxCreateLimit is the maximum number of sandbox create requests per minute.
	SandboxCreateLimit int32 `json:"sandboxCreateLimit" protobuf:"varint,1,opt,name=sandboxCreateLimit"`
	// SandboxCreateBurst is the burst limit for sandbox create requests.
	SandboxCreateBurst int32 `json:"sandboxCreateBurst" protobuf:"varint,2,opt,name=sandboxCreateBurst"`
	// APICallLimit is the maximum number of API calls per minute.
	// +optional
	APICallLimit int32 `json:"apiCallLimit,omitempty" protobuf:"varint,3,opt,name=apiCallLimit"`
	// APICallBurst is the burst limit for API calls.
	// +optional
	APICallBurst int32 `json:"apiCallBurst,omitempty" protobuf:"varint,4,opt,name=apiCallBurst"`
}

// TenantStatus defines the observed state of a Tenant.
type TenantStatus struct {
	// Phase is the current phase of the tenant.
	Phase TenantPhase `json:"phase" protobuf:"bytes,1,opt,name=phase"`

	// ActiveSandboxes is the number of currently active sandboxes.
	ActiveSandboxes int32 `json:"activeSandboxes,omitempty" protobuf:"varint,2,opt,name=activeSandboxes"`

	// TotalSandboxesCreated is the total number of sandboxes ever created.
	TotalSandboxesCreated int64 `json:"totalSandboxesCreated,omitempty" protobuf:"varint,3,opt,name=totalSandboxesCreated"`

	// ResourceUsage contains the current aggregate resource usage.
	ResourceUsage *AggregateResourceUsage `json:"resourceUsage,omitempty" protobuf:"bytes,4,opt,name=resourceUsage"`

	// QuotaUsage contains the current quota usage.
	QuotaUsage *QuotaUsage `json:"quotaUsage,omitempty" protobuf:"bytes,5,opt,name=quotaUsage"`

	// Conditions represents the latest observations of the tenant's state.
	Conditions []TenantCondition `json:"conditions,omitempty" protobuf:"bytes,6,rep,name=conditions"`

	// LastSandboxCreateTime is the time when the last sandbox was created.
	// +optional
	LastSandboxCreateTime *metav1.Time `json:"lastSandboxCreateTime,omitempty" protobuf:"bytes,7,opt,name=lastSandboxCreateTime"`

	// SandboxesCreatedToday is the number of sandboxes created today.
	SandboxesCreatedToday int32 `json:"sandboxesCreatedToday,omitempty" protobuf:"varint,8,opt,name=sandboxesCreatedToday"`

	// CostSummary contains cost summary information.
	// +optional
	CostSummary *TenantCostSummary `json:"costSummary,omitempty" protobuf:"bytes,9,opt,name=costSummary"`
}

// AggregateResourceUsage contains aggregate resource usage for a tenant.
type AggregateResourceUsage struct {
	// CPUUsage is the total CPU usage in cores.
	CPUUsage string `json:"cpuUsage,omitempty" protobuf:"bytes,1,opt,name=cpuUsage"`
	// MemoryUsageBytes is the total memory usage in bytes.
	MemoryUsageBytes uint64 `json:"memoryUsageBytes,omitempty" protobuf:"varint,2,opt,name=memoryUsageBytes"`
	// GPUUsage is the total GPU usage.
	GPUUsage string `json:"gpuUsage,omitempty" protobuf:"bytes,3,opt,name=gpuUsage"`
	// StorageUsageBytes is the total storage usage in bytes.
	StorageUsageBytes uint64 `json:"storageUsageBytes,omitempty" protobuf:"varint,4,opt,name=storageUsageBytes"`
	// NetworkRxBytes is the total received bytes.
	NetworkRxBytes uint64 `json:"networkRxBytes,omitempty" protobuf:"varint,5,opt,name=networkRxBytes"`
	// NetworkTxBytes is the total transmitted bytes.
	NetworkTxBytes uint64 `json:"networkTxBytes,omitempty" protobuf:"varint,6,opt,name=networkTxBytes"`
}

// QuotaUsage contains the current quota usage for a tenant.
type QuotaUsage struct {
	// CPUUsed is the currently used CPU.
	CPUUsed string `json:"cpuUsed,omitempty" protobuf:"bytes,1,opt,name=cpuUsed"`
	// CPULimit is the CPU quota limit.
	CPULimit string `json:"cpuLimit,omitempty" protobuf:"bytes,2,opt,name=cpuLimit"`
	// MemoryUsedBytes is the currently used memory in bytes.
	MemoryUsedBytes uint64 `json:"memoryUsedBytes,omitempty" protobuf:"varint,3,opt,name=memoryUsedBytes"`
	// MemoryLimitBytes is the memory quota limit in bytes.
	MemoryLimitBytes uint64 `json:"memoryLimitBytes,omitempty" protobuf:"varint,4,opt,name=memoryLimitBytes"`
	// InstanceUsed is the currently used instance count.
	InstanceUsed int32 `json:"instanceUsed,omitempty" protobuf:"varint,5,opt,name=instanceUsed"`
	// InstanceLimit is the instance quota limit.
	InstanceLimit int32 `json:"instanceLimit,omitempty" protobuf:"varint,6,opt,name=instanceLimit"`
	// GPUUsed is the currently used GPU count.
	GPUUsed string `json:"gpuUsed,omitempty" protobuf:"bytes,7,opt,name=gpuUsed"`
	// GPULimit is the GPU quota limit.
	GPULimit string `json:"gpuLimit,omitempty" protobuf:"bytes,8,opt,name=gpuLimit"`
}

// TenantCondition describes the state of a tenant at a certain point.
type TenantCondition struct {
	// Type of tenant condition.
	Type TenantConditionType `json:"type" protobuf:"bytes,1,opt,name=type"`
	// Status of the condition.
	Status ConditionStatus `json:"status" protobuf:"bytes,2,opt,name=status"`
	// Last time the condition transitioned.
	// +optional
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty" protobuf:"bytes,3,opt,name=lastTransitionTime"`
	// The reason for the condition's last transition.
	// +optional
	Reason string `json:"reason,omitempty" protobuf:"bytes,4,opt,name=reason"`
	// A human-readable message.
	// +optional
	Message string `json:"message,omitempty" protobuf:"bytes,5,opt,name=message"`
}

// TenantConditionType represents a tenant condition type.
type TenantConditionType string

const (
	// TenantConditionQuotaAvailable indicates whether the tenant has quota available.
	TenantConditionQuotaAvailable TenantConditionType = "QuotaAvailable"
	// TenantConditionRateLimitOK indicates whether the tenant is within rate limits.
	TenantConditionRateLimitOK TenantConditionType = "RateLimitOK"
	// TenantConditionResourcesHealthy indicates whether the tenant's resources are healthy.
	TenantConditionResourcesHealthy TenantConditionType = "ResourcesHealthy"
	// TenantConditionBillingOK indicates whether the tenant's billing is in good standing.
	TenantConditionBillingOK TenantConditionType = "BillingOK"
)

// TenantCostSummary contains cost summary information for a tenant.
type TenantCostSummary struct {
	// DailyCost is the estimated daily cost.
	DailyCost float64 `json:"dailyCost,omitempty" protobuf:"fixed64,1,opt,name=dailyCost"`
	// MonthlyCost is the accumulated monthly cost.
	MonthlyCost float64 `json:"monthlyCost,omitempty" protobuf:"fixed64,2,opt,name=monthlyCost"`
	// CostByResource breaks down cost by resource type.
	CostByResource map[string]float64 `json:"costByResource,omitempty" protobuf:"bytes,3,rep,name=costByResource"`
	// CostByNode breaks down cost by node.
	CostByNode map[string]float64 `json:"costByNode,omitempty" protobuf:"bytes,4,rep,name=costByNode"`
	// LastUpdated is the last time the cost was calculated.
	LastUpdated metav1.Time `json:"lastUpdated,omitempty" protobuf:"bytes,5,opt,name=lastUpdated"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=tn
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Active",type=integer,JSONPath=`.status.activeSandboxes`
// +kubebuilder:printcolumn:name="CPU",type=string,JSONPath=`.spec.resourceQuota.cpu`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Tenant is the Schema for the tenants API.
type Tenant struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TenantSpec   `json:"spec,omitempty"`
	Status TenantStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// TenantList contains a list of Tenant.
type TenantList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Tenant `json:"items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=sq

// SandboxQuota is the Schema for the sandboxquotas API.
type SandboxQuota struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec SandboxQuotaSpec `json:"spec,omitempty"`
}

// SandboxQuotaSpec defines the desired state of SandboxQuota.
type SandboxQuotaSpec struct {
	// TenantRef references the tenant this quota applies to.
	TenantRef corev1.ObjectReference `json:"tenantRef" protobuf:"bytes,1,opt,name=tenantRef"`
	// Hard defines the hard quota limits.
	Hard TenantResourceQuota `json:"hard" protobuf:"bytes,2,opt,name=hard"`
	// Used defines the currently used resources.
	// +optional
	Used TenantResourceQuota `json:"used,omitempty" protobuf:"bytes,3,opt,name=used"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// SandboxQuotaList contains a list of SandboxQuota.
type SandboxQuotaList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []SandboxQuota `json:"items"`
}
