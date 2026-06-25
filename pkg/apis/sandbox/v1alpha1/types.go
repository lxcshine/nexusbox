// Package v1alpha1 contains the schema definitions for the NexusBox sandbox API v1alpha1.
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SandboxPhase represents the current lifecycle phase of a sandbox.
type SandboxPhase string

const (
	// SandboxPending indicates the sandbox has been created but not yet scheduled.
	SandboxPending SandboxPhase = "Pending"
	// SandboxScheduling indicates the sandbox is being evaluated by the scheduler.
	SandboxScheduling SandboxPhase = "Scheduling"
	// SandboxCreating indicates the sandbox runtime is being created on the target node.
	SandboxCreating SandboxPhase = "Creating"
	// SandboxRunning indicates the sandbox is active and running.
	SandboxRunning SandboxPhase = "Running"
	// SandboxPausing indicates the sandbox is transitioning to paused state.
	SandboxPausing SandboxPhase = "Pausing"
	// SandboxPaused indicates the sandbox has been paused (frozen).
	SandboxPaused SandboxPhase = "Paused"
	// SandboxResuming indicates the sandbox is transitioning from paused to running.
	SandboxResuming SandboxPhase = "Resuming"
	// SandboxStopping indicates the sandbox is being gracefully stopped.
	SandboxStopping SandboxPhase = "Stopping"
	// SandboxStopped indicates the sandbox has been stopped but not deleted.
	SandboxStopped SandboxPhase = "Stopped"
	// SandboxTerminating indicates the sandbox is being deleted.
	SandboxTerminating SandboxPhase = "Terminating"
	// SandboxFailed indicates the sandbox encountered an unrecoverable error.
	SandboxFailed SandboxPhase = "Failed"
	// SandboxEvicted indicates the sandbox was evicted from its node.
	SandboxEvicted SandboxPhase = "Evicted"
)

// IsTerminal returns true if the phase represents a terminal state.
func (p SandboxPhase) IsTerminal() bool {
	return p == SandboxFailed || p == SandboxEvicted
}

// SandboxRuntimeType represents the type of runtime used for sandbox isolation.
type SandboxRuntimeType string

const (
	// RuntimeKataContainers uses Kata Containers for VM-level isolation.
	RuntimeKataContainers SandboxRuntimeType = "kata-containers"
	// RuntimeGVisor uses gVisor for application-level isolation.
	RuntimeGVisor SandboxRuntimeType = "gvisor"
	// RuntimeRunc uses standard runc containers (weakest isolation).
	RuntimeRunc SandboxRuntimeType = "runc"
)

// SandboxPriority represents the scheduling priority of a sandbox.
type SandboxPriority int32

const (
	// PriorityLow is for non-critical batch sandboxes.
	PriorityLow SandboxPriority = 0
	// PriorityNormal is for standard sandboxes.
	PriorityNormal SandboxPriority = 50
	// PriorityHigh is for important sandboxes.
	PriorityHigh SandboxPriority = 75
	// PriorityCritical is for mission-critical sandboxes.
	PriorityCritical SandboxPriority = 100
)

// SandboxSchedulingPolicy defines the scheduling strategy for a sandbox.
type SandboxSchedulingPolicy string

const (
	// ScheduleBestEffort schedules to any available node.
	ScheduleBestEffort SandboxSchedulingPolicy = "BestEffort"
	// ScheduleBinPack packs sandboxes tightly to minimize resource waste.
	ScheduleBinPack SandboxSchedulingPolicy = "BinPack"
	// ScheduleSpread spreads sandboxes across nodes for availability.
	ScheduleSpread SandboxSchedulingPolicy = "Spread"
	// ScheduleTenantAffinity prefers nodes already running same-tenant sandboxes.
	ScheduleTenantAffinity SandboxSchedulingPolicy = "TenantAffinity"
	// ScheduleTenantAntiAffinity avoids nodes running same-tenant sandboxes.
	ScheduleTenantAntiAffinity SandboxSchedulingPolicy = "TenantAntiAffinity"
)

// SandboxSpec defines the desired state of a Sandbox.
type SandboxSpec struct {
	// TenantRef references the tenant that owns this sandbox.
	TenantRef TenantReference `json:"tenantRef" protobuf:"bytes,1,opt,name=tenantRef"`

	// TemplateRef references a SandboxTemplate to use for this sandbox.
	// +optional
	TemplateRef *SandboxTemplateReference `json:"templateRef,omitempty" protobuf:"bytes,2,opt,name=templateRef"`

	// Runtime specifies the isolation runtime type.
	Runtime SandboxRuntimeType `json:"runtime" protobuf:"bytes,3,opt,name=runtime"`

	// Priority determines scheduling priority.
	Priority SandboxPriority `json:"priority" protobuf:"varint,4,opt,name=priority"`

	// SchedulingPolicy defines how the sandbox should be scheduled.
	SchedulingPolicy SandboxSchedulingPolicy `json:"schedulingPolicy" protobuf:"bytes,5,opt,name=schedulingPolicy"`

	// Resources defines the resource requirements for this sandbox.
	Resources ResourceRequirements `json:"resources" protobuf:"bytes,6,opt,name=resources"`

	// Image specifies the container image to run inside the sandbox.
	Image string `json:"image" protobuf:"bytes,7,opt,name=image"`

	// Command specifies the command to run inside the sandbox.
	// +optional
	Command []string `json:"command,omitempty" protobuf:"bytes,8,rep,name=command"`

	// Args specifies the arguments to the command.
	// +optional
	Args []string `json:"args,omitempty" protobuf:"bytes,9,rep,name=args"`

	// Env specifies environment variables for the sandbox.
	// +optional
	Env []EnvVar `json:"env,omitempty" protobuf:"bytes,10,rep,name=env"`

	// WorkingDir specifies the working directory inside the sandbox.
	// +optional
	WorkingDir string `json:"workingDir,omitempty" protobuf:"bytes,11,opt,name=workingDir"`

	// NodeSelector specifies node affinity constraints.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty" protobuf:"bytes,12,rep,name=nodeSelector"`

	// NodeAffinity specifies node affinity scheduling rules.
	// +optional
	NodeAffinity *NodeAffinity `json:"nodeAffinity,omitempty" protobuf:"bytes,13,opt,name=nodeAffinity"`

	// Tolerations specifies node tolerations.
	// +optional
	Tolerations []Toleration `json:"tolerations,omitempty" protobuf:"bytes,14,rep,name=tolerations"`

	// MaxLifetimeSeconds specifies the maximum lifetime of the sandbox.
	// After this duration, the sandbox will be automatically terminated.
	// +optional
	MaxLifetimeSeconds *int64 `json:"maxLifetimeSeconds,omitempty" protobuf:"varint,15,opt,name=maxLifetimeSeconds"`

	// IdleTimeoutSeconds specifies the idle timeout after which the sandbox
	// will be automatically paused to save resources.
	// +optional
	IdleTimeoutSeconds *int64 `json:"idleTimeoutSeconds,omitempty" protobuf:"varint,16,opt,name=idleTimeoutSeconds"`

	// AutoDeleteOnCompletion indicates whether to delete the sandbox after it completes.
	// +optional
	AutoDeleteOnCompletion bool `json:"autoDeleteOnCompletion,omitempty" protobuf:"varint,17,opt,name=autoDeleteOnCompletion"`

	// Network specifies the network configuration for the sandbox.
	// +optional
	Network *SandboxNetworkSpec `json:"network,omitempty" protobuf:"bytes,18,opt,name=network"`

	// Storage specifies the storage configuration for the sandbox.
	// +optional
	Storage *SandboxStorageSpec `json:"storage,omitempty" protobuf:"bytes,19,opt,name=storage"`

	// Security specifies the security configuration for the sandbox.
	// +optional
	Security *SandboxSecuritySpec `json:"security,omitempty" protobuf:"bytes,20,opt,name=security"`

	// BatchInfo contains batch scheduling information.
	// +optional
	BatchInfo *BatchSchedulingInfo `json:"batchInfo,omitempty" protobuf:"bytes,21,opt,name=batchInfo"`

	// RestartPolicy defines the restart behavior of the sandbox.
	RestartPolicy RestartPolicy `json:"restartPolicy" protobuf:"bytes,22,opt,name=restartPolicy"`

	// GracefulShutdownSeconds specifies the grace period for shutdown.
	// +optional
	GracefulShutdownSeconds *int64 `json:"gracefulShutdownSeconds,omitempty" protobuf:"varint,23,opt,name=gracefulShutdownSeconds"`
}

// RestartPolicy defines the restart behavior of a sandbox.
type RestartPolicy string

const (
	// RestartPolicyNever never restarts the sandbox.
	RestartPolicyNever RestartPolicy = "Never"
	// RestartPolicyOnFailure restarts the sandbox on failure.
	RestartPolicyOnFailure RestartPolicy = "OnFailure"
	// RestartPolicyAlways always restarts the sandbox.
	RestartPolicyAlways RestartPolicy = "Always"
)

// TenantReference references a tenant resource.
type TenantReference struct {
	// Name of the tenant.
	Name string `json:"name" protobuf:"bytes,1,opt,name=name"`
	// Namespace of the tenant resource.
	// +optional
	Namespace string `json:"namespace,omitempty" protobuf:"bytes,2,opt,name=namespace"`
}

// SandboxTemplateReference references a SandboxTemplate.
type SandboxTemplateReference struct {
	// Name of the SandboxTemplate.
	Name string `json:"name" protobuf:"bytes,1,opt,name=name"`
	// Namespace of the SandboxTemplate.
	// +optional
	Namespace string `json:"namespace,omitempty" protobuf:"bytes,2,opt,name=namespace"`
}

// ResourceRequirements defines the resource requirements for a sandbox.
type ResourceRequirements struct {
	// CPU specifies the CPU requirement in cores.
	CPU string `json:"cpu" protobuf:"bytes,1,opt,name=cpu"`
	// Memory specifies the memory requirement.
	Memory string `json:"memory" protobuf:"bytes,2,opt,name=memory"`
	// EphemeralStorage specifies the ephemeral storage requirement.
	// +optional
	EphemeralStorage string `json:"ephemeralStorage,omitempty" protobuf:"bytes,3,opt,name=ephemeralStorage"`
	// GPU specifies the GPU requirement.
	// +optional
	GPU string `json:"gpu,omitempty" protobuf:"bytes,4,opt,name=gpu"`
	// Limits specifies resource limits (upper bounds).
	// +optional
	Limits ResourceList `json:"limits,omitempty" protobuf:"bytes,5,rep,name=limits"`
	// Requests specifies resource requests (guaranteed minimum).
	// +optional
	Requests ResourceList `json:"requests,omitempty" protobuf:"bytes,6,rep,name=requests"`
}

// ResourceList is a map of resource name to quantity.
type ResourceList map[string]string

// EnvVar represents an environment variable.
type EnvVar struct {
	// Name of the environment variable.
	Name string `json:"name" protobuf:"bytes,1,opt,name=name"`
	// Value of the environment variable.
	// +optional
	Value string `json:"value,omitempty" protobuf:"bytes,2,opt,name=value"`
	// ValueFrom specifies a source for the environment variable's value.
	// +optional
	ValueFrom *EnvVarSource `json:"valueFrom,omitempty" protobuf:"bytes,3,opt,name=valueFrom"`
}

// EnvVarSource represents a source for the value of an EnvVar.
type EnvVarSource struct {
	// SecretKeyRef selects a key from a Secret.
	// +optional
	SecretKeyRef *SecretKeySelector `json:"secretKeyRef,omitempty" protobuf:"bytes,1,opt,name=secretKeyRef"`
	// ConfigMapKeyRef selects a key from a ConfigMap.
	// +optional
	ConfigMapKeyRef *ConfigMapKeySelector `json:"configMapKeyRef,omitempty" protobuf:"bytes,2,opt,name=configMapKeyRef"`
}

// SecretKeySelector selects a key from a Secret.
type SecretKeySelector struct {
	// Name of the Secret.
	Name string `json:"name" protobuf:"bytes,1,opt,name=name"`
	// Key within the Secret.
	Key string `json:"key" protobuf:"bytes,2,opt,name=key"`
}

// ConfigMapKeySelector selects a key from a ConfigMap.
type ConfigMapKeySelector struct {
	// Name of the ConfigMap.
	Name string `json:"name" protobuf:"bytes,1,opt,name=name"`
	// Key within the ConfigMap.
	Key string `json:"key" protobuf:"bytes,2,opt,name=key"`
}

// NodeAffinity describes node affinity scheduling rules.
type NodeAffinity struct {
	// RequiredDuringSchedulingIgnoredDuringExecution defines hard node constraints.
	// +optional
	RequiredDuringSchedulingIgnoredDuringExecution *NodeSelector `json:"requiredDuringSchedulingIgnoredDuringExecution,omitempty" protobuf:"bytes,1,opt,name=requiredDuringSchedulingIgnoredDuringExecution"`
	// PreferredDuringSchedulingIgnoredDuringExecution defines soft node preferences.
	// +optional
	PreferredDuringSchedulingIgnoredDuringExecution []PreferredSchedulingTerm `json:"preferredDuringSchedulingIgnoredDuringExecution,omitempty" protobuf:"bytes,2,rep,name=preferredDuringSchedulingIgnoredDuringExecution"`
}

// NodeSelector represents the union of the results of one or more label queries
// over a set of nodes.
type NodeSelector struct {
	// NodeSelectorTerms is a list of node selector terms.
	NodeSelectorTerms []NodeSelectorTerm `json:"nodeSelectorTerms" protobuf:"bytes,1,rep,name=nodeSelectorTerms"`
}

// NodeSelectorTerm represents a set of node selector requirements.
type NodeSelectorTerm struct {
	// MatchExpressions is a list of node selector requirements by label.
	// +optional
	MatchExpressions []NodeSelectorRequirement `json:"matchExpressions,omitempty" protobuf:"bytes,1,rep,name=matchExpressions"`
	// MatchFields is a list of node selector requirements by field.
	// +optional
	MatchFields []NodeSelectorRequirement `json:"matchFields,omitempty" protobuf:"bytes,2,rep,name=matchFields"`
}

// NodeSelectorRequirement is a selector that contains values, a key, and an operator.
type NodeSelectorRequirement struct {
	// Key is the label key or field key.
	Key string `json:"key" protobuf:"bytes,1,opt,name=key"`
	// Operator represents a key's relationship to a set of values.
	Operator NodeSelectorOperator `json:"operator" protobuf:"bytes,2,opt,name=operator"`
	// Values is an array of string values.
	// +optional
	Values []string `json:"values,omitempty" protobuf:"bytes,3,rep,name=values"`
}

// NodeSelectorOperator is the set of operators that can be used in a node selector requirement.
type NodeSelectorOperator string

const (
	// NodeSelectorOpIn matches if the key is in the values.
	NodeSelectorOpIn NodeSelectorOperator = "In"
	// NodeSelectorOpNotIn matches if the key is not in the values.
	NodeSelectorOpNotIn NodeSelectorOperator = "NotIn"
	// NodeSelectorOpExists matches if the key exists.
	NodeSelectorOpExists NodeSelectorOperator = "Exists"
	// NodeSelectorOpDoesNotExist matches if the key does not exist.
	NodeSelectorOpDoesNotExist NodeSelectorOperator = "DoesNotExist"
	// NodeSelectorOpGt matches if the key is greater than the value.
	NodeSelectorOpGt NodeSelectorOperator = "Gt"
	// NodeSelectorOpLt matches if the key is less than the value.
	NodeSelectorOpLt NodeSelectorOperator = "Lt"
)

// PreferredSchedulingTerm represents an optional preference.
type PreferredSchedulingTerm struct {
	// Weight associated with matching the corresponding nodeSelectorTerm.
	Weight int32 `json:"weight" protobuf:"varint,1,opt,name=weight"`
	// Preference is a node selector term.
	Preference NodeSelectorTerm `json:"preference" protobuf:"bytes,2,opt,name=preference"`
}

// Toleration represents a toleration for a node taint.
type Toleration struct {
	// Key is the taint key that the toleration applies to.
	// +optional
	Key string `json:"key,omitempty" protobuf:"bytes,1,opt,name=key"`
	// Operator represents the relationship between the key and value.
	// +optional
	Operator TolerationOperator `json:"operator,omitempty" protobuf:"bytes,2,opt,name=operator"`
	// Value is the taint value the toleration matches.
	// +optional
	Value string `json:"value,omitempty" protobuf:"bytes,3,opt,name=value"`
	// Effect indicates the taint effect to match.
	// +optional
	Effect TaintEffect `json:"effect,omitempty" protobuf:"bytes,4,opt,name=effect"`
	// TolerationSeconds represents the period of time the toleration tolerates the taint.
	// +optional
	TolerationSeconds *int64 `json:"tolerationSeconds,omitempty" protobuf:"varint,5,opt,name=tolerationSeconds"`
}

// TolerationOperator is the set of operators that can be used in a toleration.
type TolerationOperator string

const (
	// TolerationOpEqual matches if the key and value are equal.
	TolerationOpEqual TolerationOperator = "Equal"
	// TolerationOpExists matches if the key exists.
	TolerationOpExists TolerationOperator = "Exists"
)

// TaintEffect is the effect of a taint on a node.
type TaintEffect string

const (
	// TaintEffectNoSchedule means the pod cannot be scheduled.
	TaintEffectNoSchedule TaintEffect = "NoSchedule"
	// TaintEffectPreferNoSchedule means the pod should not be scheduled.
	TaintEffectPreferNoSchedule TaintEffect = "PreferNoSchedule"
	// TaintEffectNoExecute means the pod will be evicted if it doesn't tolerate the taint.
	TaintEffectNoExecute TaintEffect = "NoExecute"
)

// SandboxNetworkSpec defines the network configuration for a sandbox.
type SandboxNetworkSpec struct {
	// NetworkMode specifies the network mode.
	NetworkMode NetworkMode `json:"networkMode" protobuf:"bytes,1,opt,name=networkMode"`
	// BandwidthLimit specifies the bandwidth limit in Mbps.
	// +optional
	BandwidthLimit string `json:"bandwidthLimit,omitempty" protobuf:"bytes,2,opt,name=bandwidthLimit"`
	// EgressRules specifies egress network rules.
	// +optional
	EgressRules []NetworkRule `json:"egressRules,omitempty" protobuf:"bytes,3,rep,name=egressRules"`
	// IngressRules specifies ingress network rules.
	// +optional
	IngressRules []NetworkRule `json:"ingressRules,omitempty" protobuf:"bytes,4,rep,name=ingressRules"`
	// DNSConfig specifies DNS configuration.
	// +optional
	DNSConfig *DNSConfig `json:"dnsConfig,omitempty" protobuf:"bytes,5,opt,name=dnsConfig"`
	// HostNetwork indicates whether to use the host network namespace.
	// +optional
	HostNetwork bool `json:"hostNetwork,omitempty" protobuf:"varint,6,opt,name=hostNetwork"`
}

// NetworkMode specifies the network isolation mode.
type NetworkMode string

const (
	// NetworkModeBridge uses bridge networking.
	NetworkModeBridge NetworkMode = "Bridge"
	// NetworkModeHost uses host networking.
	NetworkModeHost NetworkMode = "Host"
	// NetworkModeNone disables networking.
	NetworkModeNone NetworkMode = "None"
	// NetworkModeCustom uses custom CNI configuration.
	NetworkModeCustom NetworkMode = "Custom"
)

// NetworkRule defines a network access rule.
type NetworkRule struct {
	// CIDR specifies the destination CIDR.
	CIDR string `json:"cidr,omitempty" protobuf:"bytes,1,opt,name=cidr"`
	// Ports specifies the destination ports.
	// +optional
	Ports []PortRange `json:"ports,omitempty" protobuf:"bytes,2,rep,name=ports"`
	// Protocol specifies the network protocol.
	Protocol Protocol `json:"protocol" protobuf:"bytes,3,opt,name=protocol"`
	// Action specifies the action (Allow or Deny).
	Action NetworkAction `json:"action" protobuf:"bytes,4,opt,name=action"`
}

// PortRange defines a range of ports.
type PortRange struct {
	// Start is the start port.
	Start int32 `json:"start" protobuf:"varint,1,opt,name=start"`
	// End is the end port (inclusive).
	End int32 `json:"end" protobuf:"varint,2,opt,name=end"`
}

// Protocol represents a network protocol.
type Protocol string

const (
	// ProtocolTCP represents TCP protocol.
	ProtocolTCP Protocol = "TCP"
	// ProtocolUDP represents UDP protocol.
	ProtocolUDP Protocol = "UDP"
	// ProtocolICMP represents ICMP protocol.
	ProtocolICMP Protocol = "ICMP"
	// ProtocolAll represents all protocols.
	ProtocolAll Protocol = "All"
)

// NetworkAction represents a network rule action.
type NetworkAction string

const (
	// NetworkActionAllow allows the traffic.
	NetworkActionAllow NetworkAction = "Allow"
	// NetworkActionDeny denies the traffic.
	NetworkActionDeny NetworkAction = "Deny"
)

// DNSConfig specifies DNS configuration.
type DNSConfig struct {
	// Nameservers specifies the DNS nameservers.
	Nameservers []string `json:"nameservers,omitempty" protobuf:"bytes,1,rep,name=nameservers"`
	// Searches specifies the DNS search domains.
	Searches []string `json:"searches,omitempty" protobuf:"bytes,2,rep,name=searches"`
	// Options specifies DNS options.
	Options []DNSOption `json:"options,omitempty" protobuf:"bytes,3,rep,name=options"`
}

// DNSOption represents a DNS option.
type DNSOption struct {
	// Name of the option.
	Name string `json:"name" protobuf:"bytes,1,opt,name=name"`
	// Value of the option.
	// +optional
	Value string `json:"value,omitempty" protobuf:"bytes,2,opt,name=value"`
}

// SandboxStorageSpec defines the storage configuration for a sandbox.
type SandboxStorageSpec struct {
	// Volumes specifies the volume mounts for the sandbox.
	// +optional
	Volumes []SandboxVolume `json:"volumes,omitempty" protobuf:"bytes,1,rep,name=volumes"`
	// EphemeralStorageLimit specifies the limit for ephemeral storage.
	// +optional
	EphemeralStorageLimit string `json:"ephemeralStorageLimit,omitempty" protobuf:"bytes,2,opt,name=ephemeralStorageLimit"`
	// RootFSSize specifies the size of the root filesystem.
	// +optional
	RootFSSize string `json:"rootFsSize,omitempty" protobuf:"bytes,3,opt,name=rootFsSize"`
}

// SandboxVolume defines a volume for a sandbox.
type SandboxVolume struct {
	// Name of the volume.
	Name string `json:"name" protobuf:"bytes,1,opt,name=name"`
	// MountPath is the path inside the sandbox where the volume is mounted.
	MountPath string `json:"mountPath" protobuf:"bytes,2,opt,name=mountPath"`
	// VolumeSource specifies the source of the volume data.
	VolumeSource VolumeSource `json:"volumeSource" protobuf:"bytes,3,opt,name=volumeSource"`
	// ReadOnly indicates whether the volume is read-only.
	// +optional
	ReadOnly bool `json:"readOnly,omitempty" protobuf:"varint,4,opt,name=readOnly"`
}

// VolumeSource represents the source of a volume.
type VolumeSource struct {
	// HostPath mounts a file or directory from the host.
	// +optional
	HostPath *HostPathVolumeSource `json:"hostPath,omitempty" protobuf:"bytes,1,opt,name=hostPath"`
	// EmptyDir uses an ephemeral directory.
	// +optional
	EmptyDir *EmptyDirVolumeSource `json:"emptyDir,omitempty" protobuf:"bytes,2,opt,name=emptyDir"`
	// PVC references a PersistentVolumeClaim.
	// +optional
	PVC *PVCVolumeSource `json:"pvc,omitempty" protobuf:"bytes,3,opt,name=pvc"`
	// Secret mounts a Secret as a volume.
	// +optional
	Secret *SecretVolumeSource `json:"secret,omitempty" protobuf:"bytes,4,opt,name=secret"`
	// ConfigMap mounts a ConfigMap as a volume.
	// +optional
	ConfigMap *ConfigMapVolumeSource `json:"configMap,omitempty" protobuf:"bytes,5,opt,name=configMap"`
}

// HostPathVolumeSource represents a host path volume.
type HostPathVolumeSource struct {
	// Path is the path on the host.
	Path string `json:"path" protobuf:"bytes,1,opt,name=path"`
	// Type is the host path type.
	// +optional
	Type HostPathType `json:"type,omitempty" protobuf:"bytes,2,opt,name=type"`
}

// HostPathType represents the type of host path.
type HostPathType string

const (
	// HostPathDirectory means a directory must exist.
	HostPathDirectory HostPathType = "Directory"
	// HostPathDirectoryOrCreate means a directory will be created if needed.
	HostPathDirectoryOrCreate HostPathType = "DirectoryOrCreate"
	// HostPathFile means a file must exist.
	HostPathFile HostPathType = "File"
	// HostPathFileOrCreate means a file will be created if needed.
	HostPathFileOrCreate HostPathType = "FileOrCreate"
)

// EmptyDirVolumeSource represents an empty directory volume.
type EmptyDirVolumeSource struct {
	// Medium specifies the storage medium.
	// +optional
	Medium StorageMedium `json:"medium,omitempty" protobuf:"bytes,1,opt,name=medium"`
	// SizeLimit specifies the total amount of local storage.
	// +optional
	SizeLimit string `json:"sizeLimit,omitempty" protobuf:"bytes,2,opt,name=sizeLimit"`
}

// StorageMedium defines the storage medium for an EmptyDir.
type StorageMedium string

const (
	// StorageMediumDefault uses the default medium.
	StorageMediumDefault StorageMedium = ""
	// StorageMediumMemory uses memory-backed storage (tmpfs).
	StorageMediumMemory StorageMedium = "Memory"
	// StorageMediumHugePages uses huge pages.
	StorageMediumHugePages StorageMedium = "HugePages"
)

// PVCVolumeSource represents a PVC volume source.
type PVCVolumeSource struct {
	// ClaimName is the name of the PVC.
	ClaimName string `json:"claimName" protobuf:"bytes,1,opt,name=claimName"`
	// ReadOnly indicates whether the volume is read-only.
	// +optional
	ReadOnly bool `json:"readOnly,omitempty" protobuf:"varint,2,opt,name=readOnly"`
}

// SecretVolumeSource represents a secret volume source.
type SecretVolumeSource struct {
	// SecretName is the name of the Secret.
	SecretName string `json:"secretName" protobuf:"bytes,1,opt,name=secretName"`
	// Items specifies key-to-path mapping.
	// +optional
	Items []KeyToPath `json:"items,omitempty" protobuf:"bytes,2,rep,name=items"`
}

// ConfigMapVolumeSource represents a ConfigMap volume source.
type ConfigMapVolumeSource struct {
	// Name is the name of the ConfigMap.
	Name string `json:"name" protobuf:"bytes,1,opt,name=name"`
	// Items specifies key-to-path mapping.
	// +optional
	Items []KeyToPath `json:"items,omitempty" protobuf:"bytes,2,rep,name=items"`
}

// KeyToPath maps a string key to a path within a volume.
type KeyToPath struct {
	// Key is the key to project.
	Key string `json:"key" protobuf:"bytes,1,opt,name=key"`
	// Path is the relative path to map the key to.
	Path string `json:"path" protobuf:"bytes,2,opt,name=path"`
	// Mode is the file mode bits.
	// +optional
	Mode int32 `json:"mode,omitempty" protobuf:"varint,3,opt,name=mode"`
}

// SandboxSecuritySpec defines the security configuration for a sandbox.
type SandboxSecuritySpec struct {
	// RunAsUser specifies the UID to run the sandbox process as.
	// +optional
	RunAsUser *int64 `json:"runAsUser,omitempty" protobuf:"varint,1,opt,name=runAsUser"`
	// RunAsGroup specifies the GID to run the sandbox process as.
	// +optional
	RunAsGroup *int64 `json:"runAsGroup,omitempty" protobuf:"varint,2,opt,name=runAsGroup"`
	// ReadOnlyRootFilesystem indicates whether the root filesystem is read-only.
	// +optional
	ReadOnlyRootFilesystem bool `json:"readOnlyRootFilesystem,omitempty" protobuf:"varint,3,opt,name=readOnlyRootFilesystem"`
	// AllowPrivilegeEscalation controls whether a process can gain more privileges.
	// +optional
	AllowPrivilegeEscalation *bool `json:"allowPrivilegeEscalation,omitempty" protobuf:"varint,4,opt,name=allowPrivilegeEscalation"`
	// Capabilities specifies the Linux capabilities.
	// +optional
	Capabilities *Capabilities `json:"capabilities,omitempty" protobuf:"bytes,5,opt,name=capabilities"`
	// SeccompProfile specifies the seccomp profile.
	// +optional
	SeccompProfile *SeccompProfile `json:"seccompProfile,omitempty" protobuf:"bytes,6,opt,name=seccompProfile"`
	// SELinuxOptions specifies the SELinux context.
	// +optional
	SELinuxOptions *SELinuxOptions `json:"seLinuxOptions,omitempty" protobuf:"bytes,7,opt,name=seLinuxOptions"`
	// AppArmorProfile specifies the AppArmor profile.
	// +optional
	AppArmorProfile *AppArmorProfile `json:"appArmorProfile,omitempty" protobuf:"bytes,8,opt,name=appArmorProfile"`
}

// Capabilities specifies the Linux capabilities for a sandbox.
type Capabilities struct {
	// Add is the list of capabilities to add.
	// +optional
	Add []string `json:"add,omitempty" protobuf:"bytes,1,rep,name=add"`
	// Drop is the list of capabilities to drop.
	// +optional
	Drop []string `json:"drop,omitempty" protobuf:"bytes,2,rep,name=drop"`
}

// SeccompProfile specifies the seccomp profile settings.
type SeccompProfile struct {
	// Type indicates the type of seccomp profile.
	Type SeccompProfileType `json:"type" protobuf:"bytes,1,opt,name=type"`
	// LocalhostProfile indicates a profile defined on the node.
	// +optional
	LocalhostProfile string `json:"localhostProfile,omitempty" protobuf:"bytes,2,opt,name=localhostProfile"`
}

// SeccompProfileType defines the type of seccomp profile.
type SeccompProfileType string

const (
	// SeccompProfileTypeUnconfined indicates no seccomp profile.
	SeccompProfileTypeUnconfined SeccompProfileType = "Unconfined"
	// SeccompProfileTypeRuntimeDefault indicates the runtime default profile.
	SeccompProfileTypeRuntimeDefault SeccompProfileType = "RuntimeDefault"
	// SeccompProfileTypeLocalhost indicates a localhost-defined profile.
	SeccompProfileTypeLocalhost SeccompProfileType = "Localhost"
)

// SELinuxOptions specifies the SELinux context.
type SELinuxOptions struct {
	// User is the SELinux user label.
	// +optional
	User string `json:"user,omitempty" protobuf:"bytes,1,opt,name=user"`
	// Role is the SELinux role label.
	// +optional
	Role string `json:"role,omitempty" protobuf:"bytes,2,opt,name=role"`
	// Type is the SELinux type label.
	// +optional
	Type string `json:"type,omitempty" protobuf:"bytes,3,opt,name=type"`
	// Level is the SELinux level label.
	// +optional
	Level string `json:"level,omitempty" protobuf:"bytes,4,opt,name=level"`
}

// AppArmorProfile specifies the AppArmor profile settings.
type AppArmorProfile struct {
	// Type indicates the type of AppArmor profile.
	Type AppArmorProfileType `json:"type" protobuf:"bytes,1,opt,name=type"`
	// LocalhostProfile indicates a profile defined on the node.
	// +optional
	LocalhostProfile string `json:"localhostProfile,omitempty" protobuf:"bytes,2,opt,name=localhostProfile"`
}

// AppArmorProfileType defines the type of AppArmor profile.
type AppArmorProfileType string

const (
	// AppArmorProfileTypeUnconfined indicates no AppArmor profile.
	AppArmorProfileTypeUnconfined AppArmorProfileType = "Unconfined"
	// AppArmorProfileTypeRuntimeDefault indicates the runtime default profile.
	AppArmorProfileTypeRuntimeDefault AppArmorProfileType = "RuntimeDefault"
	// AppArmorProfileTypeLocalhost indicates a localhost-defined profile.
	AppArmorProfileTypeLocalhost AppArmorProfileType = "Localhost"
)

// BatchSchedulingInfo contains batch scheduling information.
type BatchSchedulingInfo struct {
	// BatchID identifies the batch this sandbox belongs to.
	BatchID string `json:"batchID" protobuf:"bytes,1,opt,name=batchID"`
	// BatchSize is the total number of sandboxes in the batch.
	BatchSize int32 `json:"batchSize" protobuf:"varint,2,opt,name=batchSize"`
	// BatchIndex is the index of this sandbox within the batch.
	BatchIndex int32 `json:"batchIndex" protobuf:"varint,3,opt,name=batchIndex"`
	// GangScheduling indicates whether gang scheduling is enabled.
	// If enabled, all sandboxes in the batch must be scheduled together.
	// +optional
	GangScheduling bool `json:"gangScheduling,omitempty" protobuf:"varint,4,opt,name=gangScheduling"`
	// MinAvailable is the minimum number of sandboxes that must be
	// scheduled for the batch to be considered successful.
	// +optional
	MinAvailable int32 `json:"minAvailable,omitempty" protobuf:"varint,5,opt,name=minAvailable"`
	// BatchPriority is the priority of the entire batch.
	// +optional
	BatchPriority SandboxPriority `json:"batchPriority,omitempty" protobuf:"varint,6,opt,name=batchPriority"`
}

// SandboxStatus defines the observed state of a Sandbox.
type SandboxStatus struct {
	// Phase is the current lifecycle phase of the sandbox.
	Phase SandboxPhase `json:"phase" protobuf:"bytes,1,opt,name=phase"`

	// NodeName is the name of the node where the sandbox is running.
	// +optional
	NodeName string `json:"nodeName,omitempty" protobuf:"bytes,2,opt,name=nodeName"`

	// NodeIP is the IP address of the node.
	// +optional
	NodeIP string `json:"nodeIP,omitempty" protobuf:"bytes,3,opt,name=nodeIP"`

	// SandboxIP is the IP address assigned to the sandbox.
	// +optional
	SandboxIP string `json:"sandboxIP,omitempty" protobuf:"bytes,4,opt,name=sandboxIP"`

	// RuntimeID is the container runtime identifier.
	// +optional
	RuntimeID string `json:"runtimeID,omitempty" protobuf:"bytes,5,opt,name=runtimeID"`

	// StartTime is the time when the sandbox started running.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty" protobuf:"bytes,6,opt,name=startTime"`

	// CompletionTime is the time when the sandbox completed.
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty" protobuf:"bytes,7,opt,name=completionTime"`

	// Conditions represents the latest available observations of the sandbox's state.
	Conditions []SandboxCondition `json:"conditions,omitempty" protobuf:"bytes,8,rep,name=conditions"`

	// ResourceUsage contains the current resource usage of the sandbox.
	// +optional
	ResourceUsage *ResourceUsage `json:"resourceUsage,omitempty" protobuf:"bytes,9,opt,name=resourceUsage"`

	// RetryCount is the number of times the sandbox has been retried.
	RetryCount int32 `json:"retryCount,omitempty" protobuf:"varint,10,opt,name=retryCount"`

	// LastScheduledTime is the last time the sandbox was scheduled.
	// +optional
	LastScheduledTime *metav1.Time `json:"lastScheduledTime,omitempty" protobuf:"bytes,11,opt,name=lastScheduledTime"`

	// Message is a human-readable message indicating details about the sandbox.
	// +optional
	Message string `json:"message,omitempty" protobuf:"bytes,12,opt,name=message"`

	// Reason is a brief CamelCase message indicating details about the sandbox.
	// +optional
	Reason string `json:"reason,omitempty" protobuf:"bytes,13,opt,name=reason"`

	// EvictionInfo contains details about an eviction.
	// +optional
	EvictionInfo *EvictionInfo `json:"evictionInfo,omitempty" protobuf:"bytes,14,opt,name=evictionInfo"`

	// CostInfo contains cost tracking information.
	// +optional
	CostInfo *CostInfo `json:"costInfo,omitempty" protobuf:"bytes,15,opt,name=costInfo"`
}

// SandboxCondition describes the state of a sandbox at a certain point.
type SandboxCondition struct {
	// Type of sandbox condition.
	Type SandboxConditionType `json:"type" protobuf:"bytes,1,opt,name=type"`
	// Status of the condition.
	Status ConditionStatus `json:"status" protobuf:"bytes,2,opt,name=status"`
	// Last time the condition transitioned from one status to another.
	// +optional
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty" protobuf:"bytes,3,opt,name=lastTransitionTime"`
	// The reason for the condition's last transition.
	// +optional
	Reason string `json:"reason,omitempty" protobuf:"bytes,4,opt,name=reason"`
	// A human-readable message indicating details about the transition.
	// +optional
	Message string `json:"message,omitempty" protobuf:"bytes,5,opt,name=message"`
}

// SandboxConditionType represents a sandbox condition type.
type SandboxConditionType string

const (
	// SandboxConditionScheduled indicates the sandbox has been scheduled.
	SandboxConditionScheduled SandboxConditionType = "Scheduled"
	// SandboxConditionReady indicates the sandbox is ready to serve.
	SandboxConditionReady SandboxConditionType = "Ready"
	// SandboxConditionResourcesAvailable indicates resources are available.
	SandboxConditionResourcesAvailable SandboxConditionType = "ResourcesAvailable"
	// SandboxConditionNetworkReady indicates the network is ready.
	SandboxConditionNetworkReady SandboxConditionType = "NetworkReady"
	// SandboxConditionStorageReady indicates the storage is ready.
	SandboxConditionStorageReady SandboxConditionType = "StorageReady"
	// SandboxConditionRuntimeReady indicates the runtime is ready.
	SandboxConditionRuntimeReady SandboxConditionType = "RuntimeReady"
	// SandboxConditionQuotaAvailable indicates the tenant quota is available.
	SandboxConditionQuotaAvailable SandboxConditionType = "QuotaAvailable"
)

// ConditionStatus represents the status of a condition.
type ConditionStatus string

const (
	// ConditionTrue means the condition is true.
	ConditionTrue ConditionStatus = "True"
	// ConditionFalse means the condition is false.
	ConditionFalse ConditionStatus = "False"
	// ConditionUnknown means the condition status is unknown.
	ConditionUnknown ConditionStatus = "Unknown"
)

// ResourceUsage contains the current resource usage of a sandbox.
type ResourceUsage struct {
	// CPUUsageNanoCores is the CPU usage in nano-cores.
	CPUUsageNanoCores uint64 `json:"cpuUsageNanoCores,omitempty" protobuf:"varint,1,opt,name=cpuUsageNanoCores"`
	// MemoryUsageBytes is the memory usage in bytes.
	MemoryUsageBytes uint64 `json:"memoryUsageBytes,omitempty" protobuf:"varint,2,opt,name=memoryUsageBytes"`
	// StorageUsageBytes is the storage usage in bytes.
	StorageUsageBytes uint64 `json:"storageUsageBytes,omitempty" protobuf:"varint,3,opt,name=storageUsageBytes"`
	// NetworkRxBytes is the total received bytes.
	NetworkRxBytes uint64 `json:"networkRxBytes,omitempty" protobuf:"varint,4,opt,name=networkRxBytes"`
	// NetworkTxBytes is the total transmitted bytes.
	NetworkTxBytes uint64 `json:"networkTxBytes,omitempty" protobuf:"varint,5,opt,name=networkTxBytes"`
	// GPUMemoryUsageBytes is the GPU memory usage in bytes.
	// +optional
	GPUMemoryUsageBytes uint64 `json:"gpuMemoryUsageBytes,omitempty" protobuf:"varint,6,opt,name=gpuMemoryUsageBytes"`
	// LastUpdateTime is the last time usage was updated.
	LastUpdateTime metav1.Time `json:"lastUpdateTime,omitempty" protobuf:"bytes,7,opt,name=lastUpdateTime"`
}

// EvictionInfo contains details about a sandbox eviction.
type EvictionInfo struct {
	// Reason for the eviction.
	Reason string `json:"reason" protobuf:"bytes,1,opt,name=reason"`
	// Message with details about the eviction.
	Message string `json:"message,omitempty" protobuf:"bytes,2,opt,name=message"`
	// Time when the eviction occurred.
	Time metav1.Time `json:"time" protobuf:"bytes,3,opt,name=time"`
	// NodeName is the node from which the sandbox was evicted.
	NodeName string `json:"nodeName" protobuf:"bytes,4,opt,name=nodeName"`
}

// CostInfo contains cost tracking information for a sandbox.
type CostInfo struct {
	// AccumulatedCostSeconds is the accumulated cost in billing seconds.
	AccumulatedCostSeconds float64 `json:"accumulatedCostSeconds,omitempty" protobuf:"fixed64,1,opt,name=accumulatedCostSeconds"`
	// ResourceCostPerSecond is the cost per second for this sandbox.
	ResourceCostPerSecond float64 `json:"resourceCostPerSecond,omitempty" protobuf:"fixed64,2,opt,name=resourceCostPerSecond"`
	// LastBillingTime is the last time billing was calculated.
	LastBillingTime metav1.Time `json:"lastBillingTime,omitempty" protobuf:"bytes,3,opt,name=lastBillingTime"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=sb
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Runtime",type=string,JSONPath=`.spec.runtime`
// +kubebuilder:printcolumn:name="Node",type=string,JSONPath=`.status.nodeName`
// +kubebuilder:printcolumn:name="Tenant",type=string,JSONPath=`.spec.tenantRef.name`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Sandbox is the Schema for the sandboxes API.
type Sandbox struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SandboxSpec   `json:"spec,omitempty"`
	Status SandboxStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// SandboxList contains a list of Sandbox.
type SandboxList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Sandbox `json:"items"`
}

// SandboxNode represents a node in the sandbox cluster.
type SandboxNode struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SandboxNodeSpec   `json:"spec,omitempty"`
	Status SandboxNodeStatus `json:"status,omitempty"`
}

// SandboxNodeSpec defines the desired state of a SandboxNode.
type SandboxNodeSpec struct {
	// Runtimes are the supported runtime types on this node.
	Runtimes []SandboxRuntimeType `json:"runtimes,omitempty" protobuf:"bytes,1,rep,name=runtimes"`
	// Resources defines the resource capacity of this node.
	Resources ResourceRequirements `json:"resources,omitempty" protobuf:"bytes,2,opt,name=resources"`
	// Labels are the node labels.
	Labels map[string]string `json:"labels,omitempty" protobuf:"bytes,3,rep,name=labels"`
	// Taints are the node taints.
	Taints []NodeTaint `json:"taints,omitempty" protobuf:"bytes,4,rep,name=taints"`
}

// SandboxNodeStatus defines the observed state of a SandboxNode.
type SandboxNodeStatus struct {
	// Phase is the current phase of the node.
	Phase NodePhase `json:"phase,omitempty" protobuf:"bytes,1,opt,name=phase"`
	// Conditions are the current conditions of the node.
	Conditions []NodeCondition `json:"conditions,omitempty" protobuf:"bytes,2,rep,name=conditions"`
	// Capacity is the total resource capacity.
	Capacity ResourceRequirements `json:"capacity,omitempty" protobuf:"bytes,3,opt,name=capacity"`
	// Allocatable is the allocatable resources.
	Allocatable ResourceRequirements `json:"allocatable,omitempty" protobuf:"bytes,4,opt,name=allocatable"`
	// SandboxCount is the number of sandboxes on this node.
	SandboxCount int32 `json:"sandboxCount,omitempty" protobuf:"varint,5,opt,name=sandboxCount"`
	// LastHeartbeatTime is the last time a heartbeat was received.
	LastHeartbeatTime metav1.Time `json:"lastHeartbeatTime,omitempty" protobuf:"bytes,6,opt,name=lastHeartbeatTime"`
}

// NodePhase is the phase of a node.
type NodePhase string

const (
	NodePending  NodePhase = "Pending"
	NodeRunning  NodePhase = "Running"
	NodeNotReady NodePhase = "NotReady"
	NodeOffline  NodePhase = "Offline"
)

// NodeCondition describes the condition of a node.
type NodeCondition struct {
	Type               string      `json:"type" protobuf:"bytes,1,opt,name=type"`
	Status             string      `json:"status" protobuf:"bytes,2,opt,name=status"`
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty" protobuf:"bytes,3,opt,name=lastTransitionTime"`
	Reason             string      `json:"reason,omitempty" protobuf:"bytes,4,opt,name=reason"`
	Message            string      `json:"message,omitempty" protobuf:"bytes,5,opt,name=message"`
}

// NodeTaint represents a taint on a node.
type NodeTaint struct {
	Key    string `json:"key" protobuf:"bytes,1,opt,name=key"`
	Value  string `json:"value,omitempty" protobuf:"bytes,2,opt,name=value"`
	Effect string `json:"effect" protobuf:"bytes,3,opt,name=effect"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// SandboxNodeList contains a list of SandboxNode.
type SandboxNodeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []SandboxNode `json:"items"`
}
