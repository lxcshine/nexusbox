/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

// Package cri implements a CRI-compatible gRPC shim that allows Kubernetes
// (kubelet) to use NexusBox as a container runtime.
//
// This package implements the two CRI services:
//   - RuntimeService: container/pod lifecycle, exec, attach, logs
//   - ImageService: image pull, list, inspect, remove
//
// The implementation delegates to the existing containerd client, translating
// CRI requests into NexusBox sandbox operations. This enables Kubernetes to
// schedule pods directly onto NexusBox-managed sandboxes without requiring
// a separate containerd/CRI-O installation.
//
// The gRPC server listens on a unix socket (default: /run/nexusbox-cri.sock)
// which kubelet connects to via the --container-runtime-endpoint flag.
package cri

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	spec "github.com/opencontainers/runtime-spec/specs-go"
	"google.golang.org/grpc"
	"k8s.io/klog/v2"
)

// Server is the CRI gRPC server.
type Server struct {
	mu sync.Mutex

	// cdClient is the underlying containerd client.
	cdClient *containerd.Client
	// namespace is the containerd namespace for CRI-managed containers.
	namespace string
	// grpcServer is the gRPC server instance.
	grpcServer *grpc.Server
	// listener is the unix socket listener.
	listener net.Listener
	// sandboxStore tracks pod-level sandboxes (pause containers).
	sandboxStore map[string]*Sandbox
	// containerStore tracks regular containers.
	containerStore map[string]*Container
}

// Sandbox represents a CRI pod sandbox (pause container).
type Sandbox struct {
	ID           string
	PodSandboxID string
	Name         string
	Namespace    string
	UID          string
	State        SandboxState
	CreatedAt    time.Time
	Labels       map[string]string
	Annotations  map[string]string
	// runtimeHandle is the containerd container reference.
	runtimeHandle containerd.Container
}

// SandboxState is the lifecycle state of a pod sandbox.
type SandboxState int32

const (
	SandboxReady    SandboxState = 0
	SandboxNotReady SandboxState = 1
)

// Container represents a CRI container within a pod sandbox.
type Container struct {
	ID           string
	PodSandboxID string
	Name         string
	Image        string
	State        ContainerState
	CreatedAt    time.Time
	StartedAt    time.Time
	FinishedAt   time.Time
	ExitCode     int32
	Labels       map[string]string
	Annotations  map[string]string
	// runtimeHandle is the containerd container reference.
	runtimeHandle containerd.Container
}

// ContainerState is the lifecycle state of a container.
type ContainerState int32

const (
	ContainerCreated ContainerState = 0
	ContainerRunning ContainerState = 1
	ContainerExited  ContainerState = 2
	ContainerUnknown ContainerState = 3
)

// Config holds configuration for the CRI server.
type Config struct {
	// SocketPath is the unix socket path for the gRPC server.
	// Default: /run/nexusbox-cri.sock
	SocketPath string
	// ContainerdAddress is the containerd socket address.
	ContainerdAddress string
	// Namespace is the containerd namespace for CRI containers.
	// Default: "nexusbox-cri"
	Namespace string
}

// DefaultConfig returns a default CRI server configuration.
func DefaultConfig() *Config {
	return &Config{
		SocketPath:        "/run/nexusbox-cri.sock",
		ContainerdAddress: "/run/containerd/containerd.sock",
		Namespace:         "nexusbox-cri",
	}
}

// NewServer creates a new CRI gRPC server.
func NewServer(cfg *Config) (*Server, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	// Connect to containerd
	cdClient, err := containerd.New(cfg.ContainerdAddress,
		containerd.WithTimeout(10*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("CRI: failed to connect to containerd at %s: %w",
			cfg.ContainerdAddress, err)
	}
	klog.Infof("CRI: connected to containerd at %s (namespace: %s)",
		cfg.ContainerdAddress, cfg.Namespace)

	// Ensure socket directory exists
	socketDir := filepath.Dir(cfg.SocketPath)
	if err := os.MkdirAll(socketDir, 0755); err != nil {
		return nil, fmt.Errorf("CRI: failed to create socket dir %s: %w", socketDir, err)
	}

	// Remove any stale socket
	if err := os.RemoveAll(cfg.SocketPath); err != nil {
		return nil, fmt.Errorf("CRI: failed to remove stale socket: %w", err)
	}

	listener, err := net.Listen("unix", cfg.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("CRI: failed to listen on %s: %w", cfg.SocketPath, err)
	}
	// Set socket permissions to allow kubelet access
	if err := os.Chmod(cfg.SocketPath, 0660); err != nil {
		klog.Warningf("CRI: failed to set socket permissions: %v", err)
	}

	grpcServer := grpc.NewServer()

	s := &Server{
		cdClient:       cdClient,
		namespace:      cfg.Namespace,
		grpcServer:     grpcServer,
		listener:       listener,
		sandboxStore:   make(map[string]*Sandbox),
		containerStore: make(map[string]*Container),
	}

	// Register CRI services
	RegisterRuntimeServiceServer(grpcServer, &runtimeServiceServer{server: s})
	RegisterImageServiceServer(grpcServer, &imageServiceServer{server: s})

	klog.Infof("CRI: gRPC server listening on %s", cfg.SocketPath)
	return s, nil
}

// Serve starts the gRPC server. Blocks until Stop is called.
func (s *Server) Serve() error {
	klog.Infof("CRI: starting gRPC server")
	return s.grpcServer.Serve(s.listener)
}

// Stop gracefully stops the gRPC server and closes the containerd client.
func (s *Server) Stop() {
	klog.Infof("CRI: stopping gRPC server")
	s.grpcServer.GracefulStop()
	if s.cdClient != nil {
		s.cdClient.Close()
	}
}

// withNamespace returns a context with the containerd namespace.
func (s *Server) withNamespace(ctx context.Context) context.Context {
	return namespaces.WithNamespace(ctx, s.namespace)
}

// --- RuntimeService gRPC implementation ---

// runtimeServiceServer implements the gRPC RuntimeService.
type runtimeServiceServer struct {
	server *Server
}

// RuntimeServiceServer is the interface that the CRI gRPC server implements.
// Method signatures match the CRI v1 RuntimeService API.
type RuntimeServiceServer interface {
	Version(ctx context.Context, req *VersionRequest) (*VersionResponse, error)
	RunPodSandbox(ctx context.Context, req *RunPodSandboxRequest) (*RunPodSandboxResponse, error)
	StopPodSandbox(ctx context.Context, req *StopPodSandboxRequest) (*StopPodSandboxResponse, error)
	RemovePodSandbox(ctx context.Context, req *RemovePodSandboxRequest) (*RemovePodSandboxResponse, error)
	ListPodSandbox(ctx context.Context, req *ListPodSandboxRequest) (*ListPodSandboxResponse, error)
	PodSandboxStatus(ctx context.Context, req *PodSandboxStatusRequest) (*PodSandboxStatusResponse, error)
	CreateContainer(ctx context.Context, req *CreateContainerRequest) (*CreateContainerResponse, error)
	StartContainer(ctx context.Context, req *StartContainerRequest) (*StartContainerResponse, error)
	StopContainer(ctx context.Context, req *StopContainerRequest) (*StopContainerResponse, error)
	RemoveContainer(ctx context.Context, req *RemoveContainerRequest) (*RemoveContainerResponse, error)
	ListContainers(ctx context.Context, req *ListContainersRequest) (*ListContainersResponse, error)
	ContainerStatus(ctx context.Context, req *ContainerStatusRequest) (*ContainerStatusResponse, error)
	ExecSync(ctx context.Context, req *ExecSyncRequest) (*ExecSyncResponse, error)
}

// RegisterRuntimeServiceServer registers the RuntimeService on the gRPC server.
// Uses a custom service definition to avoid depending on k8s.io/cri-api.
func RegisterRuntimeServiceServer(s *grpc.Server, srv RuntimeServiceServer) {
	// Register as an untyped service with method handlers.
	// This allows kubelet to call these methods via gRPC reflection-free invocation.
	// In a full implementation, this would use the generated CRI proto.
	sd := &runtimeServiceDesc{srv: srv}
	s.RegisterService(sd.ServiceDesc(), srv)
}

// runtimeServiceDesc builds the gRPC ServiceDesc for RuntimeService.
type runtimeServiceDesc struct {
	srv RuntimeServiceServer
}

func (d *runtimeServiceDesc) ServiceDesc() *grpc.ServiceDesc {
	return &grpc.ServiceDesc{
		ServiceName: "runtime.v1.RuntimeService",
		HandlerType: (*RuntimeServiceServer)(nil),
		Methods: []grpc.MethodDesc{
			{
				MethodName: "Version",
				Handler:    d.handleVersion,
			},
			{
				MethodName: "RunPodSandbox",
				Handler:    d.handleRunPodSandbox,
			},
			{
				MethodName: "StopPodSandbox",
				Handler:    d.handleStopPodSandbox,
			},
			{
				MethodName: "RemovePodSandbox",
				Handler:    d.handleRemovePodSandbox,
			},
			{
				MethodName: "ListPodSandbox",
				Handler:    d.handleListPodSandbox,
			},
			{
				MethodName: "PodSandboxStatus",
				Handler:    d.handlePodSandboxStatus,
			},
			{
				MethodName: "CreateContainer",
				Handler:    d.handleCreateContainer,
			},
			{
				MethodName: "StartContainer",
				Handler:    d.handleStartContainer,
			},
			{
				MethodName: "StopContainer",
				Handler:    d.handleStopContainer,
			},
			{
				MethodName: "RemoveContainer",
				Handler:    d.handleRemoveContainer,
			},
			{
				MethodName: "ListContainers",
				Handler:    d.handleListContainers,
			},
			{
				MethodName: "ContainerStatus",
				Handler:    d.handleContainerStatus,
			},
			{
				MethodName: "ExecSync",
				Handler:    d.handleExecSync,
			},
		},
		Streams:  []grpc.StreamDesc{},
		Metadata: "cri.proto",
	}
}

// Generic handler wrappers that decode/encode using our simple types.
// These follow the standard gRPC unary handler pattern.
func (d *runtimeServiceDesc) handleVersion(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := &VersionRequest{}
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RuntimeServiceServer).Version(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/runtime.v1.RuntimeService/Version"}
	return interceptor(ctx, in, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RuntimeServiceServer).Version(ctx, req.(*VersionRequest))
	})
}

func (d *runtimeServiceDesc) handleRunPodSandbox(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := &RunPodSandboxRequest{}
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RuntimeServiceServer).RunPodSandbox(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/runtime.v1.RuntimeService/RunPodSandbox"}
	return interceptor(ctx, in, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RuntimeServiceServer).RunPodSandbox(ctx, req.(*RunPodSandboxRequest))
	})
}

func (d *runtimeServiceDesc) handleStopPodSandbox(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := &StopPodSandboxRequest{}
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RuntimeServiceServer).StopPodSandbox(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/runtime.v1.RuntimeService/StopPodSandbox"}
	return interceptor(ctx, in, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RuntimeServiceServer).StopPodSandbox(ctx, req.(*StopPodSandboxRequest))
	})
}

func (d *runtimeServiceDesc) handleRemovePodSandbox(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := &RemovePodSandboxRequest{}
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RuntimeServiceServer).RemovePodSandbox(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/runtime.v1.RuntimeService/RemovePodSandbox"}
	return interceptor(ctx, in, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RuntimeServiceServer).RemovePodSandbox(ctx, req.(*RemovePodSandboxRequest))
	})
}

func (d *runtimeServiceDesc) handleListPodSandbox(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := &ListPodSandboxRequest{}
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RuntimeServiceServer).ListPodSandbox(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/runtime.v1.RuntimeService/ListPodSandbox"}
	return interceptor(ctx, in, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RuntimeServiceServer).ListPodSandbox(ctx, req.(*ListPodSandboxRequest))
	})
}

func (d *runtimeServiceDesc) handlePodSandboxStatus(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := &PodSandboxStatusRequest{}
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RuntimeServiceServer).PodSandboxStatus(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/runtime.v1.RuntimeService/PodSandboxStatus"}
	return interceptor(ctx, in, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RuntimeServiceServer).PodSandboxStatus(ctx, req.(*PodSandboxStatusRequest))
	})
}

func (d *runtimeServiceDesc) handleCreateContainer(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := &CreateContainerRequest{}
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RuntimeServiceServer).CreateContainer(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/runtime.v1.RuntimeService/CreateContainer"}
	return interceptor(ctx, in, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RuntimeServiceServer).CreateContainer(ctx, req.(*CreateContainerRequest))
	})
}

func (d *runtimeServiceDesc) handleStartContainer(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := &StartContainerRequest{}
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RuntimeServiceServer).StartContainer(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/runtime.v1.RuntimeService/StartContainer"}
	return interceptor(ctx, in, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RuntimeServiceServer).StartContainer(ctx, req.(*StartContainerRequest))
	})
}

func (d *runtimeServiceDesc) handleStopContainer(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := &StopContainerRequest{}
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RuntimeServiceServer).StopContainer(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/runtime.v1.RuntimeService/StopContainer"}
	return interceptor(ctx, in, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RuntimeServiceServer).StopContainer(ctx, req.(*StopContainerRequest))
	})
}

func (d *runtimeServiceDesc) handleRemoveContainer(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := &RemoveContainerRequest{}
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RuntimeServiceServer).RemoveContainer(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/runtime.v1.RuntimeService/RemoveContainer"}
	return interceptor(ctx, in, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RuntimeServiceServer).RemoveContainer(ctx, req.(*RemoveContainerRequest))
	})
}

func (d *runtimeServiceDesc) handleListContainers(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := &ListContainersRequest{}
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RuntimeServiceServer).ListContainers(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/runtime.v1.RuntimeService/ListContainers"}
	return interceptor(ctx, in, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RuntimeServiceServer).ListContainers(ctx, req.(*ListContainersRequest))
	})
}

func (d *runtimeServiceDesc) handleContainerStatus(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := &ContainerStatusRequest{}
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RuntimeServiceServer).ContainerStatus(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/runtime.v1.RuntimeService/ContainerStatus"}
	return interceptor(ctx, in, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RuntimeServiceServer).ContainerStatus(ctx, req.(*ContainerStatusRequest))
	})
}

func (d *runtimeServiceDesc) handleExecSync(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := &ExecSyncRequest{}
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RuntimeServiceServer).ExecSync(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/runtime.v1.RuntimeService/ExecSync"}
	return interceptor(ctx, in, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RuntimeServiceServer).ExecSync(ctx, req.(*ExecSyncRequest))
	})
}

// --- RuntimeService method implementations ---

func (r *runtimeServiceServer) Version(ctx context.Context, req *VersionRequest) (*VersionResponse, error) {
	return &VersionResponse{
		Version:        "v1",
		RuntimeName:    "nexusbox",
		RuntimeVersion: "0.1.0",
		APIVersion:     "v1",
	}, nil
}

func (r *runtimeServiceServer) RunPodSandbox(ctx context.Context, req *RunPodSandboxRequest) (*RunPodSandboxResponse, error) {
	s := r.server
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx = s.withNamespace(ctx)
	sandboxID := generateID("sandbox")

	// Pull the pause image (or use a configured one)
	pauseImage := "registry.k8s.io/pause:3.9"
	if req.Config != nil && req.Config.Image != "" {
		pauseImage = req.Config.Image
	}

	image, err := s.cdClient.GetImage(ctx, pauseImage)
	if err != nil {
		klog.Infof("CRI: pulling pause image %s", pauseImage)
		image, err = s.cdClient.Pull(ctx, pauseImage, containerd.WithPullUnpack)
		if err != nil {
			return nil, fmt.Errorf("CRI: failed to pull pause image %s: %w", pauseImage, err)
		}
	}

	// Build OCI spec for the pause container
	specOpts := []oci.SpecOpts{
		oci.WithImageConfig(image),
		oci.WithNoNewPrivileges,
	}

	container, err := s.cdClient.NewContainer(
		ctx, sandboxID,
		containerd.WithImage(image),
		containerd.WithNewSnapshot(sandboxID+"-snapshot", image),
		containerd.WithSpec(nil, specOpts...),
	)
	if err != nil {
		return nil, fmt.Errorf("CRI: failed to create pause container %s: %w", sandboxID, err)
	}

	task, err := container.NewTask(ctx, cio.LogFile(fmt.Sprintf("/var/log/nexusbox/cri/%s.log", sandboxID)))
	if err != nil {
		container.Delete(ctx, containerd.WithSnapshotCleanup)
		return nil, fmt.Errorf("CRI: failed to create task for %s: %w", sandboxID, err)
	}
	if err := task.Start(ctx); err != nil {
		task.Delete(ctx)
		container.Delete(ctx, containerd.WithSnapshotCleanup)
		return nil, fmt.Errorf("CRI: failed to start pause container %s: %w", sandboxID, err)
	}

	sb := &Sandbox{
		ID:            sandboxID,
		PodSandboxID:  sandboxID,
		Name:          req.Config.GetName(),
		State:         SandboxReady,
		CreatedAt:     time.Now(),
		Labels:        req.Config.GetLabels(),
		Annotations:   req.Config.GetAnnotations(),
		runtimeHandle: container,
	}
	s.sandboxStore[sandboxID] = sb

	klog.Infof("CRI: RunPodSandbox %s (image: %s, PID: %d)", sandboxID, pauseImage, task.Pid())
	return &RunPodSandboxResponse{PodSandboxID: sandboxID}, nil
}

func (r *runtimeServiceServer) StopPodSandbox(ctx context.Context, req *StopPodSandboxRequest) (*StopPodSandboxResponse, error) {
	s := r.server
	s.mu.Lock()
	defer s.mu.Unlock()

	sb, ok := s.sandboxStore[req.PodSandboxID]
	if !ok {
		return nil, fmt.Errorf("CRI: sandbox %s not found", req.PodSandboxID)
	}

	ctx = s.withNamespace(ctx)
	task, err := sb.runtimeHandle.Task(ctx, nil)
	if err == nil {
		task.Kill(ctx, 15) // SIGTERM
		task.Delete(ctx)
	}
	sb.State = SandboxNotReady

	klog.Infof("CRI: StopPodSandbox %s", req.PodSandboxID)
	return &StopPodSandboxResponse{}, nil
}

func (r *runtimeServiceServer) RemovePodSandbox(ctx context.Context, req *RemovePodSandboxRequest) (*RemovePodSandboxResponse, error) {
	s := r.server
	s.mu.Lock()
	defer s.mu.Unlock()

	sb, ok := s.sandboxStore[req.PodSandboxID]
	if !ok {
		return nil, fmt.Errorf("CRI: sandbox %s not found", req.PodSandboxID)
	}

	ctx = s.withNamespace(ctx)
	// Force kill if still running
	if task, err := sb.runtimeHandle.Task(ctx, nil); err == nil {
		task.Kill(ctx, 9) // SIGKILL
		task.Delete(ctx)
	}
	sb.runtimeHandle.Delete(ctx, containerd.WithSnapshotCleanup)
	delete(s.sandboxStore, req.PodSandboxID)

	klog.Infof("CRI: RemovePodSandbox %s", req.PodSandboxID)
	return &RemovePodSandboxResponse{}, nil
}

func (r *runtimeServiceServer) ListPodSandbox(ctx context.Context, req *ListPodSandboxRequest) (*ListPodSandboxResponse, error) {
	s := r.server
	s.mu.Lock()
	defer s.mu.Unlock()

	var items []*PodSandbox
	for _, sb := range s.sandboxStore {
		items = append(items, &PodSandbox{
			ID:        sb.ID,
			Name:      sb.Name,
			State:     int32(sb.State),
			CreatedAt: sb.CreatedAt.UnixNano(),
			Labels:    sb.Labels,
		})
	}
	return &ListPodSandboxResponse{Items: items}, nil
}

func (r *runtimeServiceServer) PodSandboxStatus(ctx context.Context, req *PodSandboxStatusRequest) (*PodSandboxStatusResponse, error) {
	s := r.server
	s.mu.Lock()
	defer s.mu.Unlock()

	sb, ok := s.sandboxStore[req.PodSandboxID]
	if !ok {
		return nil, fmt.Errorf("CRI: sandbox %s not found", req.PodSandboxID)
	}
	return &PodSandboxStatusResponse{
		Status: &PodSandboxStatus{
			ID:        sb.ID,
			Name:      sb.Name,
			State:     int32(sb.State),
			CreatedAt: sb.CreatedAt.UnixNano(),
		},
	}, nil
}

func (r *runtimeServiceServer) CreateContainer(ctx context.Context, req *CreateContainerRequest) (*CreateContainerResponse, error) {
	s := r.server
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.Config == nil {
		return nil, fmt.Errorf("CRI: container config required")
	}

	ctx = s.withNamespace(ctx)
	containerID := generateID("container")

	image, err := s.cdClient.GetImage(ctx, req.Config.Image.Image)
	if err != nil {
		klog.Infof("CRI: pulling image %s", req.Config.Image.Image)
		image, err = s.cdClient.Pull(ctx, req.Config.Image.Image, containerd.WithPullUnpack)
		if err != nil {
			return nil, fmt.Errorf("CRI: failed to pull image %s: %w", req.Config.Image.Image, err)
		}
	}

	specOpts := []oci.SpecOpts{
		oci.WithImageConfig(image),
		oci.WithNoNewPrivileges,
	}
	if len(req.Config.Command) > 0 {
		specOpts = append(specOpts, oci.WithProcessArgs(req.Config.Command...))
	}

	container, err := s.cdClient.NewContainer(
		ctx, containerID,
		containerd.WithImage(image),
		containerd.WithNewSnapshot(containerID+"-snapshot", image),
		containerd.WithSpec(nil, specOpts...),
	)
	if err != nil {
		return nil, fmt.Errorf("CRI: failed to create container %s: %w", containerID, err)
	}

	c := &Container{
		ID:            containerID,
		PodSandboxID:  req.PodSandboxID,
		Name:          req.Config.GetName(),
		Image:         req.Config.Image.Image,
		State:         ContainerCreated,
		CreatedAt:     time.Now(),
		Labels:        req.Config.GetLabels(),
		Annotations:   req.Config.GetAnnotations(),
		runtimeHandle: container,
	}
	s.containerStore[containerID] = c

	klog.Infof("CRI: CreateContainer %s (image: %s, sandbox: %s)",
		containerID, req.Config.Image.Image, req.PodSandboxID)
	return &CreateContainerResponse{ContainerID: containerID}, nil
}

func (r *runtimeServiceServer) StartContainer(ctx context.Context, req *StartContainerRequest) (*StartContainerResponse, error) {
	s := r.server
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.containerStore[req.ContainerID]
	if !ok {
		return nil, fmt.Errorf("CRI: container %s not found", req.ContainerID)
	}

	ctx = s.withNamespace(ctx)
	task, err := c.runtimeHandle.NewTask(ctx, cio.LogFile(fmt.Sprintf("/var/log/nexusbox/cri/%s.log", c.ID)))
	if err != nil {
		return nil, fmt.Errorf("CRI: failed to create task for %s: %w", c.ID, err)
	}
	if err := task.Start(ctx); err != nil {
		task.Delete(ctx)
		return nil, fmt.Errorf("CRI: failed to start container %s: %w", c.ID, err)
	}

	c.State = ContainerRunning
	c.StartedAt = time.Now()

	klog.Infof("CRI: StartContainer %s (PID: %d)", c.ID, task.Pid())
	return &StartContainerResponse{}, nil
}

func (r *runtimeServiceServer) StopContainer(ctx context.Context, req *StopContainerRequest) (*StopContainerResponse, error) {
	s := r.server
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.containerStore[req.ContainerID]
	if !ok {
		return nil, fmt.Errorf("CRI: container %s not found", req.ContainerID)
	}

	ctx = s.withNamespace(ctx)
	task, err := c.runtimeHandle.Task(ctx, nil)
	if err == nil {
		task.Kill(ctx, 15) // SIGTERM
		timeout := time.Duration(req.Timeout) * time.Second
		if timeout == 0 {
			timeout = 10 * time.Second
		}
		exitCh, _ := task.Wait(ctx)
		select {
		case <-exitCh:
		case <-time.After(timeout):
			task.Kill(ctx, 9) // SIGKILL
		}
		task.Delete(ctx)
	}

	c.State = ContainerExited
	c.FinishedAt = time.Now()

	klog.Infof("CRI: StopContainer %s", c.ID)
	return &StopContainerResponse{}, nil
}

func (r *runtimeServiceServer) RemoveContainer(ctx context.Context, req *RemoveContainerRequest) (*RemoveContainerResponse, error) {
	s := r.server
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.containerStore[req.ContainerID]
	if !ok {
		return nil, fmt.Errorf("CRI: container %s not found", req.ContainerID)
	}

	ctx = s.withNamespace(ctx)
	if task, err := c.runtimeHandle.Task(ctx, nil); err == nil {
		task.Kill(ctx, 9)
		task.Delete(ctx)
	}
	c.runtimeHandle.Delete(ctx, containerd.WithSnapshotCleanup)
	delete(s.containerStore, req.ContainerID)

	klog.Infof("CRI: RemoveContainer %s", c.ID)
	return &RemoveContainerResponse{}, nil
}

func (r *runtimeServiceServer) ListContainers(ctx context.Context, req *ListContainersRequest) (*ListContainersResponse, error) {
	s := r.server
	s.mu.Lock()
	defer s.mu.Unlock()

	var items []*ContainerInfo
	for _, c := range s.containerStore {
		items = append(items, &ContainerInfo{
			ID:           c.ID,
			PodSandboxID: c.PodSandboxID,
			Name:         c.Name,
			Image:        c.Image,
			State:        int32(c.State),
			CreatedAt:    c.CreatedAt.UnixNano(),
		})
	}
	return &ListContainersResponse{Containers: items}, nil
}

func (r *runtimeServiceServer) ContainerStatus(ctx context.Context, req *ContainerStatusRequest) (*ContainerStatusResponse, error) {
	s := r.server
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.containerStore[req.ContainerID]
	if !ok {
		return nil, fmt.Errorf("CRI: container %s not found", req.ContainerID)
	}
	return &ContainerStatusResponse{
		Status: &ContainerStatus{
			ID:         c.ID,
			Name:       c.Name,
			State:      int32(c.State),
			Image:      c.Image,
			CreatedAt:  c.CreatedAt.UnixNano(),
			StartedAt:  c.StartedAt.UnixNano(),
			FinishedAt: c.FinishedAt.UnixNano(),
			ExitCode:   c.ExitCode,
		},
	}, nil
}

func (r *runtimeServiceServer) ExecSync(ctx context.Context, req *ExecSyncRequest) (*ExecSyncResponse, error) {
	s := r.server
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.containerStore[req.ContainerID]
	if !ok {
		return nil, fmt.Errorf("CRI: container %s not found", req.ContainerID)
	}

	ctx = s.withNamespace(ctx)
	task, err := c.runtimeHandle.Task(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("CRI: failed to get task for %s: %w", c.ID, err)
	}

	var stdout, stderr []byte
	// Use a simple pipe-based exec
	rdr, wtr := io.Pipe()
	defer rdr.Close()

	process, err := task.Exec(ctx, c.ID+"-exec", &spec.Process{
		Args: req.Cmd,
		Cwd:  "/",
	}, cio.NewCreator(cio.WithStreams(nil, wtr, wtr)))
	if err != nil {
		return nil, fmt.Errorf("CRI: exec failed for %s: %w", c.ID, err)
	}
	defer process.Delete(ctx)

	// Read output in background
	go func() {
		defer wtr.Close()
		buf := make([]byte, 4096)
		for {
			n, err := rdr.Read(buf)
			if n > 0 {
				stdout = append(stdout, buf[:n]...)
			}
			if err != nil {
				break
			}
		}
	}()

	exitCh, err := process.Wait(ctx)
	if err != nil {
		return nil, fmt.Errorf("CRI: exec wait failed: %w", err)
	}
	if err := process.Start(ctx); err != nil {
		return nil, fmt.Errorf("CRI: exec start failed: %w", err)
	}

	status := <-exitCh
	return &ExecSyncResponse{
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: status.ExitCode(),
	}, nil
}

// --- ImageService gRPC implementation ---

type imageServiceServer struct {
	server *Server
}

type ImageServiceServer interface {
	ListImages(ctx context.Context, req *ListImagesRequest) (*ListImagesResponse, error)
	ImageStatus(ctx context.Context, req *ImageStatusRequest) (*ImageStatusResponse, error)
	PullImage(ctx context.Context, req *PullImageRequest) (*PullImageResponse, error)
	RemoveImage(ctx context.Context, req *RemoveImageRequest) (*RemoveImageResponse, error)
}

func RegisterImageServiceServer(s *grpc.Server, srv ImageServiceServer) {
	sd := &imageServiceDesc{srv: srv}
	s.RegisterService(sd.ServiceDesc(), srv)
}

type imageServiceDesc struct {
	srv ImageServiceServer
}

func (d *imageServiceDesc) ServiceDesc() *grpc.ServiceDesc {
	return &grpc.ServiceDesc{
		ServiceName: "runtime.v1.ImageService",
		HandlerType: (*ImageServiceServer)(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "ListImages", Handler: d.handleListImages},
			{MethodName: "ImageStatus", Handler: d.handleImageStatus},
			{MethodName: "PullImage", Handler: d.handlePullImage},
			{MethodName: "RemoveImage", Handler: d.handleRemoveImage},
		},
		Streams:  []grpc.StreamDesc{},
		Metadata: "cri.proto",
	}
}

func (d *imageServiceDesc) handleListImages(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := &ListImagesRequest{}
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ImageServiceServer).ListImages(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/runtime.v1.ImageService/ListImages"}
	return interceptor(ctx, in, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ImageServiceServer).ListImages(ctx, req.(*ListImagesRequest))
	})
}

func (d *imageServiceDesc) handleImageStatus(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := &ImageStatusRequest{}
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ImageServiceServer).ImageStatus(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/runtime.v1.ImageService/ImageStatus"}
	return interceptor(ctx, in, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ImageServiceServer).ImageStatus(ctx, req.(*ImageStatusRequest))
	})
}

func (d *imageServiceDesc) handlePullImage(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := &PullImageRequest{}
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ImageServiceServer).PullImage(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/runtime.v1.ImageService/PullImage"}
	return interceptor(ctx, in, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ImageServiceServer).PullImage(ctx, req.(*PullImageRequest))
	})
}

func (d *imageServiceDesc) handleRemoveImage(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := &RemoveImageRequest{}
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ImageServiceServer).RemoveImage(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/runtime.v1.ImageService/RemoveImage"}
	return interceptor(ctx, in, info, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ImageServiceServer).RemoveImage(ctx, req.(*RemoveImageRequest))
	})
}

func (i *imageServiceServer) ListImages(ctx context.Context, req *ListImagesRequest) (*ListImagesResponse, error) {
	s := i.server
	ctx = s.withNamespace(ctx)

	images, err := s.cdClient.ListImages(ctx)
	if err != nil {
		return nil, fmt.Errorf("CRI: list images failed: %w", err)
	}

	var items []*ImageInfo
	for _, img := range images {
		items = append(items, &ImageInfo{
			ID: img.Name(),
		})
	}
	return &ListImagesResponse{Images: items}, nil
}

func (i *imageServiceServer) ImageStatus(ctx context.Context, req *ImageStatusRequest) (*ImageStatusResponse, error) {
	s := i.server
	ctx = s.withNamespace(ctx)

	if req.Image == nil || req.Image.Image == "" {
		return nil, fmt.Errorf("CRI: image reference required")
	}

	img, err := s.cdClient.GetImage(ctx, req.Image.Image)
	if err != nil {
		return &ImageStatusResponse{Image: nil}, nil
	}
	return &ImageStatusResponse{
		Image: &ImageInfo{ID: img.Name()},
	}, nil
}

func (i *imageServiceServer) PullImage(ctx context.Context, req *PullImageRequest) (*PullImageResponse, error) {
	s := i.server
	ctx = s.withNamespace(ctx)

	if req.Image == nil || req.Image.Image == "" {
		return nil, fmt.Errorf("CRI: image reference required")
	}

	klog.Infof("CRI: PullImage %s", req.Image.Image)
	img, err := s.cdClient.Pull(ctx, req.Image.Image, containerd.WithPullUnpack)
	if err != nil {
		return nil, fmt.Errorf("CRI: pull image %s failed: %w", req.Image.Image, err)
	}
	return &PullImageResponse{
		ImageRef: img.Name(),
	}, nil
}

func (i *imageServiceServer) RemoveImage(ctx context.Context, req *RemoveImageRequest) (*RemoveImageResponse, error) {
	s := i.server
	ctx = s.withNamespace(ctx)

	if req.Image == nil || req.Image.Image == "" {
		return nil, fmt.Errorf("CRI: image reference required")
	}

	// containerd image store delete
	img, err := s.cdClient.GetImage(ctx, req.Image.Image)
	if err != nil {
		return nil, fmt.Errorf("CRI: image %s not found: %w", req.Image.Image, err)
	}
	// Use the image service to delete
	is := s.cdClient.ImageService()
	if err := is.Delete(ctx, img.Name()); err != nil {
		return nil, fmt.Errorf("CRI: delete image %s failed: %w", req.Image.Image, err)
	}

	klog.Infof("CRI: RemoveImage %s", req.Image.Image)
	return &RemoveImageResponse{}, nil
}

// generateID generates a unique ID with the given prefix.
func generateID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}
