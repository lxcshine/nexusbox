package v1alpha1

import "k8s.io/apimachinery/pkg/runtime"

// DeepCopyInto copies all receiver into out. The out object must be created before calling this method.
func (in *Sandbox) DeepCopyInto(out *Sandbox) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy creates a deep copy of the Sandbox.
func (in *Sandbox) DeepCopy() *Sandbox {
	if in == nil {
		return nil
	}
	out := new(Sandbox)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject creates a deep copy of the Sandbox as a runtime.Object.
func (in *Sandbox) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies all receiver into out.
func (in *SandboxList) DeepCopyInto(out *SandboxList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]Sandbox, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy creates a deep copy of the SandboxList.
func (in *SandboxList) DeepCopy() *SandboxList {
	if in == nil {
		return nil
	}
	out := new(SandboxList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject creates a deep copy of the SandboxList as a runtime.Object.
func (in *SandboxList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies all receiver into out.
func (in *SandboxSpec) DeepCopyInto(out *SandboxSpec) {
	*out = *in
	out.TenantRef = in.TenantRef
	if in.TemplateRef != nil {
		in, out := &in.TemplateRef, &out.TemplateRef
		*out = new(SandboxTemplateReference)
		**out = **in
	}
	out.Resources = in.Resources
	if in.Command != nil {
		in, out := &in.Command, &out.Command
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Args != nil {
		in, out := &in.Args, &out.Args
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Env != nil {
		in, out := &in.Env, &out.Env
		*out = make([]EnvVar, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.NodeSelector != nil {
		in, out := &in.NodeSelector, &out.NodeSelector
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.NodeAffinity != nil {
		in, out := &in.NodeAffinity, &out.NodeAffinity
		*out = new(NodeAffinity)
		(*in).DeepCopyInto(*out)
	}
	if in.Tolerations != nil {
		in, out := &in.Tolerations, &out.Tolerations
		*out = make([]Toleration, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.MaxLifetimeSeconds != nil {
		in, out := &in.MaxLifetimeSeconds, &out.MaxLifetimeSeconds
		*out = new(int64)
		**out = **in
	}
	if in.IdleTimeoutSeconds != nil {
		in, out := &in.IdleTimeoutSeconds, &out.IdleTimeoutSeconds
		*out = new(int64)
		**out = **in
	}
	if in.Network != nil {
		in, out := &in.Network, &out.Network
		*out = new(SandboxNetworkSpec)
		(*in).DeepCopyInto(*out)
	}
	if in.Storage != nil {
		in, out := &in.Storage, &out.Storage
		*out = new(SandboxStorageSpec)
		(*in).DeepCopyInto(*out)
	}
	if in.Security != nil {
		in, out := &in.Security, &out.Security
		*out = new(SandboxSecuritySpec)
		(*in).DeepCopyInto(*out)
	}
	if in.BatchInfo != nil {
		in, out := &in.BatchInfo, &out.BatchInfo
		*out = new(BatchSchedulingInfo)
		**out = **in
	}
	if in.GracefulShutdownSeconds != nil {
		in, out := &in.GracefulShutdownSeconds, &out.GracefulShutdownSeconds
		*out = new(int64)
		**out = **in
	}
}

// DeepCopy creates a deep copy of the SandboxSpec.
func (in *SandboxSpec) DeepCopy() *SandboxSpec {
	if in == nil {
		return nil
	}
	out := new(SandboxSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all receiver into out.
func (in *SandboxStatus) DeepCopyInto(out *SandboxStatus) {
	*out = *in
	if in.StartTime != nil {
		in, out := &in.StartTime, &out.StartTime
		*out = (*in).DeepCopy()
	}
	if in.CompletionTime != nil {
		in, out := &in.CompletionTime, &out.CompletionTime
		*out = (*in).DeepCopy()
	}
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]SandboxCondition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.ResourceUsage != nil {
		in, out := &in.ResourceUsage, &out.ResourceUsage
		*out = new(ResourceUsage)
		(*in).DeepCopyInto(*out)
	}
	if in.LastScheduledTime != nil {
		in, out := &in.LastScheduledTime, &out.LastScheduledTime
		*out = (*in).DeepCopy()
	}
	if in.EvictionInfo != nil {
		in, out := &in.EvictionInfo, &out.EvictionInfo
		*out = new(EvictionInfo)
		(*in).DeepCopyInto(*out)
	}
	if in.CostInfo != nil {
		in, out := &in.CostInfo, &out.CostInfo
		*out = new(CostInfo)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy creates a deep copy of the SandboxStatus.
func (in *SandboxStatus) DeepCopy() *SandboxStatus {
	if in == nil {
		return nil
	}
	out := new(SandboxStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all receiver into out.
func (in *SandboxCondition) DeepCopyInto(out *SandboxCondition) {
	*out = *in
	in.LastTransitionTime.DeepCopyInto(&out.LastTransitionTime)
}

// DeepCopyInto copies all receiver into out.
func (in *ResourceUsage) DeepCopyInto(out *ResourceUsage) {
	*out = *in
	in.LastUpdateTime.DeepCopyInto(&out.LastUpdateTime)
}

// DeepCopyInto copies all receiver into out.
func (in *EvictionInfo) DeepCopyInto(out *EvictionInfo) {
	*out = *in
	in.Time.DeepCopyInto(&out.Time)
}

// DeepCopyInto copies all receiver into out.
func (in *CostInfo) DeepCopyInto(out *CostInfo) {
	*out = *in
	in.LastBillingTime.DeepCopyInto(&out.LastBillingTime)
}

// DeepCopyInto copies all receiver into out.
func (in *EnvVar) DeepCopyInto(out *EnvVar) {
	*out = *in
	if in.ValueFrom != nil {
		in, out := &in.ValueFrom, &out.ValueFrom
		*out = new(EnvVarSource)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopyInto copies all receiver into out.
func (in *EnvVarSource) DeepCopyInto(out *EnvVarSource) {
	*out = *in
	if in.SecretKeyRef != nil {
		in, out := &in.SecretKeyRef, &out.SecretKeyRef
		*out = new(SecretKeySelector)
		**out = **in
	}
	if in.ConfigMapKeyRef != nil {
		in, out := &in.ConfigMapKeyRef, &out.ConfigMapKeyRef
		*out = new(ConfigMapKeySelector)
		**out = **in
	}
}

// DeepCopyInto copies all receiver into out.
func (in *NodeAffinity) DeepCopyInto(out *NodeAffinity) {
	*out = *in
	if in.RequiredDuringSchedulingIgnoredDuringExecution != nil {
		in, out := &in.RequiredDuringSchedulingIgnoredDuringExecution, &out.RequiredDuringSchedulingIgnoredDuringExecution
		*out = new(NodeSelector)
		(*in).DeepCopyInto(*out)
	}
	if in.PreferredDuringSchedulingIgnoredDuringExecution != nil {
		in, out := &in.PreferredDuringSchedulingIgnoredDuringExecution, &out.PreferredDuringSchedulingIgnoredDuringExecution
		*out = make([]PreferredSchedulingTerm, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopyInto copies all receiver into out.
func (in *NodeSelector) DeepCopyInto(out *NodeSelector) {
	*out = *in
	if in.NodeSelectorTerms != nil {
		in, out := &in.NodeSelectorTerms, &out.NodeSelectorTerms
		*out = make([]NodeSelectorTerm, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopyInto copies all receiver into out.
func (in *NodeSelectorTerm) DeepCopyInto(out *NodeSelectorTerm) {
	*out = *in
	if in.MatchExpressions != nil {
		in, out := &in.MatchExpressions, &out.MatchExpressions
		*out = make([]NodeSelectorRequirement, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.MatchFields != nil {
		in, out := &in.MatchFields, &out.MatchFields
		*out = make([]NodeSelectorRequirement, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopyInto copies all receiver into out.
func (in *NodeSelectorRequirement) DeepCopyInto(out *NodeSelectorRequirement) {
	*out = *in
	if in.Values != nil {
		in, out := &in.Values, &out.Values
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopyInto copies all receiver into out.
func (in *PreferredSchedulingTerm) DeepCopyInto(out *PreferredSchedulingTerm) {
	*out = *in
	in.Preference.DeepCopyInto(&out.Preference)
}

// DeepCopyInto copies all receiver into out.
func (in *Toleration) DeepCopyInto(out *Toleration) {
	*out = *in
	if in.TolerationSeconds != nil {
		in, out := &in.TolerationSeconds, &out.TolerationSeconds
		*out = new(int64)
		**out = **in
	}
}

// DeepCopyInto copies all receiver into out.
func (in *SandboxNetworkSpec) DeepCopyInto(out *SandboxNetworkSpec) {
	*out = *in
	if in.EgressRules != nil {
		in, out := &in.EgressRules, &out.EgressRules
		*out = make([]NetworkRule, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.IngressRules != nil {
		in, out := &in.IngressRules, &out.IngressRules
		*out = make([]NetworkRule, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.DNSConfig != nil {
		in, out := &in.DNSConfig, &out.DNSConfig
		*out = new(DNSConfig)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopyInto copies all receiver into out.
func (in *NetworkRule) DeepCopyInto(out *NetworkRule) {
	*out = *in
	if in.Ports != nil {
		in, out := &in.Ports, &out.Ports
		*out = make([]PortRange, len(*in))
		copy(*out, *in)
	}
}

// DeepCopyInto copies all receiver into out.
func (in *DNSConfig) DeepCopyInto(out *DNSConfig) {
	*out = *in
	if in.Nameservers != nil {
		in, out := &in.Nameservers, &out.Nameservers
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Searches != nil {
		in, out := &in.Searches, &out.Searches
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Options != nil {
		in, out := &in.Options, &out.Options
		*out = make([]DNSOption, len(*in))
		copy(*out, *in)
	}
}

// DeepCopyInto copies all receiver into out.
func (in *SandboxStorageSpec) DeepCopyInto(out *SandboxStorageSpec) {
	*out = *in
	if in.Volumes != nil {
		in, out := &in.Volumes, &out.Volumes
		*out = make([]SandboxVolume, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopyInto copies all receiver into out.
func (in *SandboxVolume) DeepCopyInto(out *SandboxVolume) {
	*out = *in
	in.VolumeSource.DeepCopyInto(&out.VolumeSource)
}

// DeepCopyInto copies all receiver into out.
func (in *VolumeSource) DeepCopyInto(out *VolumeSource) {
	*out = *in
	if in.HostPath != nil {
		in, out := &in.HostPath, &out.HostPath
		*out = new(HostPathVolumeSource)
		**out = **in
	}
	if in.EmptyDir != nil {
		in, out := &in.EmptyDir, &out.EmptyDir
		*out = new(EmptyDirVolumeSource)
		**out = **in
	}
	if in.PVC != nil {
		in, out := &in.PVC, &out.PVC
		*out = new(PVCVolumeSource)
		**out = **in
	}
	if in.Secret != nil {
		in, out := &in.Secret, &out.Secret
		*out = new(SecretVolumeSource)
		(*in).DeepCopyInto(*out)
	}
	if in.ConfigMap != nil {
		in, out := &in.ConfigMap, &out.ConfigMap
		*out = new(ConfigMapVolumeSource)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopyInto copies all receiver into out.
func (in *SecretVolumeSource) DeepCopyInto(out *SecretVolumeSource) {
	*out = *in
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]KeyToPath, len(*in))
		copy(*out, *in)
	}
}

// DeepCopyInto copies all receiver into out.
func (in *ConfigMapVolumeSource) DeepCopyInto(out *ConfigMapVolumeSource) {
	*out = *in
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]KeyToPath, len(*in))
		copy(*out, *in)
	}
}

// DeepCopyInto copies all receiver into out.
func (in *SandboxSecuritySpec) DeepCopyInto(out *SandboxSecuritySpec) {
	*out = *in
	if in.RunAsUser != nil {
		in, out := &in.RunAsUser, &out.RunAsUser
		*out = new(int64)
		**out = **in
	}
	if in.RunAsGroup != nil {
		in, out := &in.RunAsGroup, &out.RunAsGroup
		*out = new(int64)
		**out = **in
	}
	if in.AllowPrivilegeEscalation != nil {
		in, out := &in.AllowPrivilegeEscalation, &out.AllowPrivilegeEscalation
		*out = new(bool)
		**out = **in
	}
	if in.Capabilities != nil {
		in, out := &in.Capabilities, &out.Capabilities
		*out = new(Capabilities)
		(*in).DeepCopyInto(*out)
	}
	if in.SeccompProfile != nil {
		in, out := &in.SeccompProfile, &out.SeccompProfile
		*out = new(SeccompProfile)
		**out = **in
	}
	if in.SELinuxOptions != nil {
		in, out := &in.SELinuxOptions, &out.SELinuxOptions
		*out = new(SELinuxOptions)
		**out = **in
	}
	if in.AppArmorProfile != nil {
		in, out := &in.AppArmorProfile, &out.AppArmorProfile
		*out = new(AppArmorProfile)
		**out = **in
	}
}

// DeepCopyInto copies all receiver into out.
func (in *Capabilities) DeepCopyInto(out *Capabilities) {
	*out = *in
	if in.Add != nil {
		in, out := &in.Add, &out.Add
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Drop != nil {
		in, out := &in.Drop, &out.Drop
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopyInto copies all receiver into out.
func (in *ResourceRequirements) DeepCopyInto(out *ResourceRequirements) {
	*out = *in
	if in.Limits != nil {
		in, out := &in.Limits, &out.Limits
		*out = make(ResourceList, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.Requests != nil {
		in, out := &in.Requests, &out.Requests
		*out = make(ResourceList, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
}

// DeepCopyInto copies all receiver into out.
func (in *BatchSchedulingInfo) DeepCopyInto(out *BatchSchedulingInfo) {
	*out = *in
}

// --- Tenant DeepCopy methods ---

// DeepCopyInto copies all receiver into out.
func (in *Tenant) DeepCopyInto(out *Tenant) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy creates a deep copy of the Tenant.
func (in *Tenant) DeepCopy() *Tenant {
	if in == nil {
		return nil
	}
	out := new(Tenant)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject creates a deep copy of the Tenant as a runtime.Object.
func (in *Tenant) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies all receiver into out.
func (in *TenantList) DeepCopyInto(out *TenantList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]Tenant, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy creates a deep copy of the TenantList.
func (in *TenantList) DeepCopy() *TenantList {
	if in == nil {
		return nil
	}
	out := new(TenantList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject creates a deep copy of the TenantList as a runtime.Object.
func (in *TenantList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies all receiver into out.
func (in *TenantSpec) DeepCopyInto(out *TenantSpec) {
	*out = *in
	if in.AdminContacts != nil {
		in, out := &in.AdminContacts, &out.AdminContacts
		*out = make([]ContactInfo, len(*in))
		copy(*out, *in)
	}
	in.ResourceQuota.DeepCopyInto(&out.ResourceQuota)
	if in.AllowedRuntimes != nil {
		in, out := &in.AllowedRuntimes, &out.AllowedRuntimes
		*out = make([]SandboxRuntimeType, len(*in))
		copy(*out, *in)
	}
	if in.AllowedSchedulingPolicies != nil {
		in, out := &in.AllowedSchedulingPolicies, &out.AllowedSchedulingPolicies
		*out = make([]SandboxSchedulingPolicy, len(*in))
		copy(*out, *in)
	}
	if in.DefaultSandboxSpec != nil {
		in, out := &in.DefaultSandboxSpec, &out.DefaultSandboxSpec
		*out = new(DefaultSandboxSpec)
		(*in).DeepCopyInto(*out)
	}
	if in.NetworkPolicy != nil {
		in, out := &in.NetworkPolicy, &out.NetworkPolicy
		*out = new(TenantNetworkPolicy)
		(*in).DeepCopyInto(*out)
	}
	if in.Labels != nil {
		in, out := &in.Labels, &out.Labels
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.RateLimit != nil {
		in, out := &in.RateLimit, &out.RateLimit
		*out = new(RateLimitConfig)
		**out = **in
	}
}

// DeepCopy creates a deep copy of the TenantSpec.
func (in *TenantSpec) DeepCopy() *TenantSpec {
	if in == nil {
		return nil
	}
	out := new(TenantSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all receiver into out.
func (in *TenantStatus) DeepCopyInto(out *TenantStatus) {
	*out = *in
	if in.ResourceUsage != nil {
		in, out := &in.ResourceUsage, &out.ResourceUsage
		*out = new(AggregateResourceUsage)
		**out = **in
	}
	if in.QuotaUsage != nil {
		in, out := &in.QuotaUsage, &out.QuotaUsage
		*out = new(QuotaUsage)
		**out = **in
	}
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]TenantCondition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.LastSandboxCreateTime != nil {
		in, out := &in.LastSandboxCreateTime, &out.LastSandboxCreateTime
		*out = (*in).DeepCopy()
	}
	if in.CostSummary != nil {
		in, out := &in.CostSummary, &out.CostSummary
		*out = new(TenantCostSummary)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy creates a deep copy of the TenantStatus.
func (in *TenantStatus) DeepCopy() *TenantStatus {
	if in == nil {
		return nil
	}
	out := new(TenantStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all receiver into out.
func (in *TenantCondition) DeepCopyInto(out *TenantCondition) {
	*out = *in
	in.LastTransitionTime.DeepCopyInto(&out.LastTransitionTime)
}

// DeepCopyInto copies all receiver into out.
func (in *TenantResourceQuota) DeepCopyInto(out *TenantResourceQuota) {
	*out = *in
	if in.ReservedResources != nil {
		in, out := &in.ReservedResources, &out.ReservedResources
		*out = new(ReservedResources)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopyInto copies all receiver into out.
func (in *ReservedResources) DeepCopyInto(out *ReservedResources) {
	*out = *in
	if in.NodeSelector != nil {
		in, out := &in.NodeSelector, &out.NodeSelector
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
}

// DeepCopyInto copies all receiver into out.
func (in *DefaultSandboxSpec) DeepCopyInto(out *DefaultSandboxSpec) {
	*out = *in
	if in.Runtime != nil {
		in, out := &in.Runtime, &out.Runtime
		*out = new(SandboxRuntimeType)
		**out = **in
	}
	if in.Priority != nil {
		in, out := &in.Priority, &out.Priority
		*out = new(SandboxPriority)
		**out = **in
	}
	if in.SchedulingPolicy != nil {
		in, out := &in.SchedulingPolicy, &out.SchedulingPolicy
		*out = new(SandboxSchedulingPolicy)
		**out = **in
	}
	if in.Resources != nil {
		in, out := &in.Resources, &out.Resources
		*out = new(ResourceRequirements)
		(*in).DeepCopyInto(*out)
	}
	if in.MaxLifetimeSeconds != nil {
		in, out := &in.MaxLifetimeSeconds, &out.MaxLifetimeSeconds
		*out = new(int64)
		**out = **in
	}
	if in.IdleTimeoutSeconds != nil {
		in, out := &in.IdleTimeoutSeconds, &out.IdleTimeoutSeconds
		*out = new(int64)
		**out = **in
	}
	if in.RestartPolicy != nil {
		in, out := &in.RestartPolicy, &out.RestartPolicy
		*out = new(RestartPolicy)
		**out = **in
	}
	if in.Security != nil {
		in, out := &in.Security, &out.Security
		*out = new(SandboxSecuritySpec)
		(*in).DeepCopyInto(*out)
	}
	if in.Network != nil {
		in, out := &in.Network, &out.Network
		*out = new(SandboxNetworkSpec)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopyInto copies all receiver into out.
func (in *TenantNetworkPolicy) DeepCopyInto(out *TenantNetworkPolicy) {
	*out = *in
	if in.AllowedEgressCIDRs != nil {
		in, out := &in.AllowedEgressCIDRs, &out.AllowedEgressCIDRs
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.DeniedEgressCIDRs != nil {
		in, out := &in.DeniedEgressCIDRs, &out.DeniedEgressCIDRs
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.AllowedIngressFromTenants != nil {
		in, out := &in.AllowedIngressFromTenants, &out.AllowedIngressFromTenants
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopyInto copies all receiver into out.
func (in *TenantCostSummary) DeepCopyInto(out *TenantCostSummary) {
	*out = *in
	if in.CostByResource != nil {
		in, out := &in.CostByResource, &out.CostByResource
		*out = make(map[string]float64, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.CostByNode != nil {
		in, out := &in.CostByNode, &out.CostByNode
		*out = make(map[string]float64, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	in.LastUpdated.DeepCopyInto(&out.LastUpdated)
}

// --- SandboxTemplate DeepCopy methods ---

// DeepCopyInto copies all receiver into out.
func (in *SandboxTemplate) DeepCopyInto(out *SandboxTemplate) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy creates a deep copy of the SandboxTemplate.
func (in *SandboxTemplate) DeepCopy() *SandboxTemplate {
	if in == nil {
		return nil
	}
	out := new(SandboxTemplate)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject creates a deep copy of the SandboxTemplate as a runtime.Object.
func (in *SandboxTemplate) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies all receiver into out.
func (in *SandboxTemplateList) DeepCopyInto(out *SandboxTemplateList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]SandboxTemplate, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy creates a deep copy of the SandboxTemplateList.
func (in *SandboxTemplateList) DeepCopy() *SandboxTemplateList {
	if in == nil {
		return nil
	}
	out := new(SandboxTemplateList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject creates a deep copy of the SandboxTemplateList as a runtime.Object.
func (in *SandboxTemplateList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies all receiver into out.
func (in *SandboxTemplateSpec) DeepCopyInto(out *SandboxTemplateSpec) {
	*out = *in
	in.Resources.DeepCopyInto(&out.Resources)
	if in.Command != nil {
		in, out := &in.Command, &out.Command
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Args != nil {
		in, out := &in.Args, &out.Args
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Env != nil {
		in, out := &in.Env, &out.Env
		*out = make([]EnvVar, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.NodeSelector != nil {
		in, out := &in.NodeSelector, &out.NodeSelector
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.Tolerations != nil {
		in, out := &in.Tolerations, &out.Tolerations
		*out = make([]Toleration, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.MaxLifetimeSeconds != nil {
		in, out := &in.MaxLifetimeSeconds, &out.MaxLifetimeSeconds
		*out = new(int64)
		**out = **in
	}
	if in.IdleTimeoutSeconds != nil {
		in, out := &in.IdleTimeoutSeconds, &out.IdleTimeoutSeconds
		*out = new(int64)
		**out = **in
	}
	if in.Network != nil {
		in, out := &in.Network, &out.Network
		*out = new(SandboxNetworkSpec)
		(*in).DeepCopyInto(*out)
	}
	if in.Storage != nil {
		in, out := &in.Storage, &out.Storage
		*out = new(SandboxStorageSpec)
		(*in).DeepCopyInto(*out)
	}
	if in.Security != nil {
		in, out := &in.Security, &out.Security
		*out = new(SandboxSecuritySpec)
		(*in).DeepCopyInto(*out)
	}
	if in.AllowedOverrides != nil {
		in, out := &in.AllowedOverrides, &out.AllowedOverrides
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopyInto copies all receiver into out.
func (in *SandboxTemplateStatus) DeepCopyInto(out *SandboxTemplateStatus) {
	*out = *in
	if in.LastUsedTime != nil {
		in, out := &in.LastUsedTime, &out.LastUsedTime
		*out = (*in).DeepCopy()
	}
}
