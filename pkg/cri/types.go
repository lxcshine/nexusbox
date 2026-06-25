package cri

// This file defines the CRI message types. These mirror the CRI v1 API
// (k8s.io/cri-api) but are plain Go structs to avoid the heavy proto
// generation dependency. For production kubelet compatibility, generate
// proper protobuf stubs from the official CRI proto definitions.

// --- Version ---

type VersionRequest struct {
	Version string `json:"version,omitempty"`
}

type VersionResponse struct {
	Version        string `json:"version"`
	RuntimeName    string `json:"runtimeName"`
	RuntimeVersion string `json:"runtimeVersion"`
	APIVersion     string `json:"apiVersion"`
}

// --- Pod Sandbox ---

type PodSandboxConfig struct {
	Name        string            `json:"name,omitempty"`
	Image       string            `json:"image,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

func (c *PodSandboxConfig) GetName() string         { return c.Name }
func (c *PodSandboxConfig) GetLabels() map[string]string { return c.Labels }
func (c *PodSandboxConfig) GetAnnotations() map[string]string { return c.Annotations }

type RunPodSandboxRequest struct {
	Config *PodSandboxConfig `json:"config,omitempty"`
}

type RunPodSandboxResponse struct {
	PodSandboxID string `json:"podSandboxId"`
}

type StopPodSandboxRequest struct {
	PodSandboxID string `json:"podSandboxId"`
}

type StopPodSandboxResponse struct{}

type RemovePodSandboxRequest struct {
	PodSandboxID string `json:"podSandboxId"`
}

type RemovePodSandboxResponse struct{}

type ListPodSandboxRequest struct{}

type PodSandbox struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	State     int32             `json:"state"`
	CreatedAt int64             `json:"createdAt"`
	Labels    map[string]string `json:"labels,omitempty"`
}

type ListPodSandboxResponse struct {
	Items []*PodSandbox `json:"items"`
}

type PodSandboxStatusRequest struct {
	PodSandboxID string `json:"podSandboxId"`
}

type PodSandboxStatus struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	State     int32  `json:"state"`
	CreatedAt int64  `json:"createdAt"`
}

type PodSandboxStatusResponse struct {
	Status *PodSandboxStatus `json:"status"`
}

// --- Container ---

type ImageSpec struct {
	Image string `json:"image"`
}

type ContainerConfig struct {
	Name        string            `json:"name,omitempty"`
	Image       *ImageSpec        `json:"image,omitempty"`
	Command     []string          `json:"command,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

func (c *ContainerConfig) GetName() string         { return c.Name }
func (c *ContainerConfig) GetLabels() map[string]string { return c.Labels }
func (c *ContainerConfig) GetAnnotations() map[string]string { return c.Annotations }

type CreateContainerRequest struct {
	PodSandboxID string           `json:"podSandboxId"`
	Config        *ContainerConfig `json:"config"`
}

type CreateContainerResponse struct {
	ContainerID string `json:"containerId"`
}

type StartContainerRequest struct {
	ContainerID string `json:"containerId"`
}

type StartContainerResponse struct{}

type StopContainerRequest struct {
	ContainerID string `json:"containerId"`
	Timeout     int64  `json:"timeout"`
}

type StopContainerResponse struct{}

type RemoveContainerRequest struct {
	ContainerID string `json:"containerId"`
}

type RemoveContainerResponse struct{}

type ListContainersRequest struct{}

type ContainerInfo struct {
	ID           string `json:"id"`
	PodSandboxID string `json:"podSandboxId"`
	Name         string `json:"name"`
	Image        string `json:"image"`
	State        int32  `json:"state"`
	CreatedAt    int64  `json:"createdAt"`
}

type ListContainersResponse struct {
	Containers []*ContainerInfo `json:"containers"`
}

type ContainerStatusRequest struct {
	ContainerID string `json:"containerId"`
}

type ContainerStatus struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	State      int32  `json:"state"`
	Image      string `json:"image"`
	CreatedAt  int64  `json:"createdAt"`
	StartedAt  int64  `json:"startedAt"`
	FinishedAt int64  `json:"finishedAt"`
	ExitCode   int32  `json:"exitCode"`
}

type ContainerStatusResponse struct {
	Status *ContainerStatus `json:"status"`
}

type ExecSyncRequest struct {
	ContainerID string   `json:"containerId"`
	Cmd         []string `json:"cmd"`
	Timeout     int64    `json:"timeout"`
}

type ExecSyncResponse struct {
	Stdout   []byte `json:"stdout"`
	Stderr   []byte `json:"stderr"`
	ExitCode uint32 `json:"exitCode"`
}

// --- Image ---

type ListImagesRequest struct {
	Filter *ImageSpec `json:"filter,omitempty"`
}

type ImageInfo struct {
	ID     string `json:"id"`
	RepoTags []string `json:"repoTags,omitempty"`
}

type ListImagesResponse struct {
	Images []*ImageInfo `json:"images"`
}

type ImageStatusRequest struct {
	Image *ImageSpec `json:"image"`
}

type ImageStatusResponse struct {
	Image *ImageInfo `json:"image,omitempty"`
}

type PullImageRequest struct {
	Image *ImageSpec `json:"image"`
}

type PullImageResponse struct {
	ImageRef string `json:"imageRef"`
}

type RemoveImageRequest struct {
	Image *ImageSpec `json:"image"`
}

type RemoveImageResponse struct{}
