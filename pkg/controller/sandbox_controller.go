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

package controller

import (
	"context"
	"fmt"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"github.com/nexusbox/nexusbox/pkg/sandbox/lifecycle"
	"github.com/nexusbox/nexusbox/pkg/sandbox/runtime"
	"github.com/nexusbox/nexusbox/pkg/tenant"
)

const (
	// SandboxControllerName is the name of the sandbox controller.
	SandboxControllerName = "sandbox-controller"

	// maxRetries is the maximum number of retries for a sandbox.
	maxRetries = 5

	// concurrentWorkers is the number of concurrent workers.
	concurrentWorkers = 5
)

// SandboxController watches Sandbox CRD objects and reconciles their state.
// It is responsible for:
// - Creating/deleting sandbox runtimes
// - Updating sandbox status
// - Handling sandbox lifecycle events
// - Coordinating with the scheduler and tenant manager
type SandboxController struct {
	mu sync.RWMutex

	// lifecycleManager manages sandbox lifecycle.
	lifecycleManager *lifecycle.LifecycleManager

	// runtimeManager manages sandbox runtimes.
	runtimeManager *runtime.RuntimeManager

	// tenantManager manages tenant information.
	tenantManager *tenant.TenantManager

	// informer watches for sandbox CRD changes.
	informer cache.SharedIndexInformer

	// queue is the work queue for sandbox reconciliation.
	queue workqueue.RateLimitingInterface

	// eventRecorder records events.
	eventRecorder EventRecorder

	// stopCh is used to signal shutdown.
	stopCh chan struct{}
}

// EventRecorder records events for sandbox objects.
type EventRecorder interface {
	RecordEvent(obj metav1.Object, eventType, reason, message string)
}

// NewSandboxController creates a new SandboxController.
func NewSandboxController(
	lifecycleManager *lifecycle.LifecycleManager,
	runtimeManager *runtime.RuntimeManager,
	tenantManager *tenant.TenantManager,
	informer cache.SharedIndexInformer,
	eventRecorder EventRecorder,
) *SandboxController {
	sc := &SandboxController{
		lifecycleManager: lifecycleManager,
		runtimeManager:   runtimeManager,
		tenantManager:    tenantManager,
		informer:         informer,
		queue:            workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "sandbox"),
		eventRecorder:    eventRecorder,
		stopCh:           make(chan struct{}),
	}

	if informer != nil {
		informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    sc.onSandboxAdd,
			UpdateFunc: sc.onSandboxUpdate,
			DeleteFunc: sc.onSandboxDelete,
		})
	}

	return sc
}

// Start starts the sandbox controller.
func (sc *SandboxController) Start(ctx context.Context) {
	klog.Info("Starting sandbox controller")

	// Start workers
	for i := 0; i < concurrentWorkers; i++ {
		go wait.Until(sc.worker, time.Second, sc.stopCh)
	}

	klog.Info("Sandbox controller started")
}

// Stop stops the sandbox controller.
func (sc *SandboxController) Stop() {
	klog.Info("Stopping sandbox controller")
	close(sc.stopCh)
	sc.queue.ShutDown()
}

// worker processes items from the work queue.
func (sc *SandboxController) worker() {
	for sc.processNextWorkItem() {
	}
}

// processNextWorkItem processes the next item from the work queue.
func (sc *SandboxController) processNextWorkItem() bool {
	key, quit := sc.queue.Get()
	if quit {
		return false
	}
	defer sc.queue.Done(key)

	err := sc.syncSandbox(key.(string))
	if err == nil {
		sc.queue.Forget(key)
		return true
	}

	if sc.queue.NumRequeues(key) < maxRetries {
		klog.Warningf("Error syncing sandbox %s, retrying: %v", key, err)
		sc.queue.AddRateLimited(key)
		return true
	}

	klog.Errorf("Error syncing sandbox %s, giving up: %v", key, err)
	sc.queue.Forget(key)
	return true
}

// syncSandbox reconciles a sandbox to its desired state.
func (sc *SandboxController) syncSandbox(key string) error {
	startTime := time.Now()
	defer func() {
		klog.V(4).Infof("Finished syncing sandbox %q (%v)", key, time.Since(startTime))
	}()

	// Get the sandbox from the informer cache
	obj, exists, err := sc.informer.GetIndexer().GetByKey(key)
	if err != nil {
		return fmt.Errorf("failed to get sandbox %s from cache: %w", key, err)
	}

	if !exists {
		// Sandbox was deleted, clean up
		klog.Infof("Sandbox %s has been deleted", key)
		return nil
	}

	sb, ok := obj.(*sandboxv1alpha1.Sandbox)
	if !ok {
		return fmt.Errorf("unexpected object type: %T", obj)
	}

	// Reconcile the sandbox based on its current phase
	return sc.reconcileSandbox(sb)
}

// reconcileSandbox reconciles a sandbox to its desired state.
func (sc *SandboxController) reconcileSandbox(sb *sandboxv1alpha1.Sandbox) error {
	switch sb.Status.Phase {
	case sandboxv1alpha1.SandboxPending:
		return sc.reconcilePendingSandbox(sb)
	case sandboxv1alpha1.SandboxScheduling:
		return sc.reconcileSchedulingSandbox(sb)
	case sandboxv1alpha1.SandboxCreating:
		return sc.reconcileCreatingSandbox(sb)
	case sandboxv1alpha1.SandboxRunning:
		return sc.reconcileRunningSandbox(sb)
	case sandboxv1alpha1.SandboxPausing:
		return sc.reconcilePausingSandbox(sb)
	case sandboxv1alpha1.SandboxPaused:
		return sc.reconcilePausedSandbox(sb)
	case sandboxv1alpha1.SandboxResuming:
		return sc.reconcileResumingSandbox(sb)
	case sandboxv1alpha1.SandboxStopping:
		return sc.reconcileStoppingSandbox(sb)
	case sandboxv1alpha1.SandboxStopped:
		return sc.reconcileStoppedSandbox(sb)
	case sandboxv1alpha1.SandboxTerminating:
		return sc.reconcileTerminatingSandbox(sb)
	case sandboxv1alpha1.SandboxFailed:
		return sc.reconcileFailedSandbox(sb)
	case sandboxv1alpha1.SandboxEvicted:
		return sc.reconcileEvictedSandbox(sb)
	default:
		klog.Warningf("Unknown sandbox phase: %s", sb.Status.Phase)
		return nil
	}
}

// reconcilePendingSandbox handles a sandbox in Pending phase.
func (sc *SandboxController) reconcilePendingSandbox(sb *sandboxv1alpha1.Sandbox) error {
	klog.V(4).Infof("Reconciling pending sandbox %s/%s", sb.Namespace, sb.Name)

	// The scheduler will pick this up and transition it to Scheduling
	return nil
}

// reconcileSchedulingSandbox handles a sandbox in Scheduling phase.
func (sc *SandboxController) reconcileSchedulingSandbox(sb *sandboxv1alpha1.Sandbox) error {
	klog.V(4).Infof("Reconciling scheduling sandbox %s/%s", sb.Namespace, sb.Name)

	// The scheduler is working on this sandbox
	return nil
}

// reconcileCreatingSandbox handles a sandbox in Creating phase.
func (sc *SandboxController) reconcileCreatingSandbox(sb *sandboxv1alpha1.Sandbox) error {
	klog.Infof("Reconciling creating sandbox %s/%s on node %s", sb.Namespace, sb.Name, sb.Status.NodeName)

	ctx := context.Background()

	// Create the runtime
	spec := sc.buildRuntimeSpec(sb)
	handle, err := sc.runtimeManager.CreateRuntime(ctx, spec)
	if err != nil {
		return fmt.Errorf("failed to create runtime for sandbox %s/%s: %w", sb.Namespace, sb.Name, err)
	}

	// Start the runtime
	key := sb.Name + "/" + sb.Namespace
	if err := sc.runtimeManager.StartRuntime(ctx, key); err != nil {
		return fmt.Errorf("failed to start runtime for sandbox %s/%s: %w", sb.Namespace, sb.Name, err)
	}

	// Mark the sandbox as running
	if err := sc.lifecycleManager.MarkSandboxRunning(ctx, key, handle); err != nil {
		return fmt.Errorf("failed to mark sandbox %s as running: %w", key, err)
	}

	// Record event
	sc.eventRecorder.RecordEvent(sb, "Normal", "SandboxStarted",
		fmt.Sprintf("Sandbox %s started on node %s", sb.Name, sb.Status.NodeName))

	return nil
}

// reconcileRunningSandbox handles a sandbox in Running phase.
func (sc *SandboxController) reconcileRunningSandbox(sb *sandboxv1alpha1.Sandbox) error {
	// Check sandbox health
	ctx := context.Background()
	key := sb.Name + "/" + sb.Namespace

	status, err := sc.runtimeManager.GetRuntimeStatus(ctx, key)
	if err != nil {
		klog.Warningf("Failed to get runtime status for sandbox %s: %v", key, err)
		return nil
	}

	if status.State == runtime.RuntimeStateError {
		// Mark sandbox as failed
		sc.lifecycleManager.MarkSandboxFailed(ctx, key, status.Error)
	}

	return nil
}

// reconcilePausingSandbox handles a sandbox in Pausing phase.
func (sc *SandboxController) reconcilePausingSandbox(sb *sandboxv1alpha1.Sandbox) error {
	ctx := context.Background()
	key := sb.Name + "/" + sb.Namespace

	if err := sc.runtimeManager.PauseRuntime(ctx, key); err != nil {
		return fmt.Errorf("failed to pause sandbox %s: %w", key, err)
	}

	return nil
}

// reconcilePausedSandbox handles a sandbox in Paused phase.
func (sc *SandboxController) reconcilePausedSandbox(sb *sandboxv1alpha1.Sandbox) error {
	// Nothing to do for a paused sandbox
	return nil
}

// reconcileResumingSandbox handles a sandbox in Resuming phase.
func (sc *SandboxController) reconcileResumingSandbox(sb *sandboxv1alpha1.Sandbox) error {
	ctx := context.Background()
	key := sb.Name + "/" + sb.Namespace

	if err := sc.runtimeManager.ResumeRuntime(ctx, key); err != nil {
		return fmt.Errorf("failed to resume sandbox %s: %w", key, err)
	}

	return nil
}

// reconcileStoppingSandbox handles a sandbox in Stopping phase.
func (sc *SandboxController) reconcileStoppingSandbox(sb *sandboxv1alpha1.Sandbox) error {
	ctx := context.Background()
	key := sb.Name + "/" + sb.Namespace

	if err := sc.runtimeManager.StopRuntime(ctx, key); err != nil {
		return fmt.Errorf("failed to stop sandbox %s: %w", key, err)
	}

	// Release tenant resources
	sc.tenantManager.ReleaseResources(sb.Spec.TenantRef.Name, sb.Status.NodeName, &sb.Spec.Resources)

	return nil
}

// reconcileStoppedSandbox handles a sandbox in Stopped phase.
func (sc *SandboxController) reconcileStoppedSandbox(sb *sandboxv1alpha1.Sandbox) error {
	// Check if the sandbox should be auto-deleted
	if sb.Spec.AutoDeleteOnCompletion {
		ctx := context.Background()
		key := sb.Name + "/" + sb.Namespace
		return sc.lifecycleManager.DeleteSandbox(ctx, key)
	}

	return nil
}

// reconcileTerminatingSandbox handles a sandbox in Terminating phase.
func (sc *SandboxController) reconcileTerminatingSandbox(sb *sandboxv1alpha1.Sandbox) error {
	key := sb.Name + "/" + sb.Namespace

	// Clean up the runtime
	sc.runtimeManager.RemoveRuntime(key)

	// Release tenant resources
	if sb.Status.NodeName != "" {
		sc.tenantManager.ReleaseResources(sb.Spec.TenantRef.Name, sb.Status.NodeName, &sb.Spec.Resources)
	}

	klog.Infof("Terminated sandbox %s", key)
	return nil
}

// reconcileFailedSandbox handles a sandbox in Failed phase.
func (sc *SandboxController) reconcileFailedSandbox(sb *sandboxv1alpha1.Sandbox) error {
	// The lifecycle manager handles retries
	return nil
}

// reconcileEvictedSandbox handles a sandbox in Evicted phase.
func (sc *SandboxController) reconcileEvictedSandbox(sb *sandboxv1alpha1.Sandbox) error {
	// The lifecycle manager will reschedule the sandbox
	return nil
}

// buildRuntimeSpec builds a RuntimeSpec from a Sandbox CRD.
func (sc *SandboxController) buildRuntimeSpec(sb *sandboxv1alpha1.Sandbox) *runtime.RuntimeSpec {
	spec := &runtime.RuntimeSpec{
		SandboxName:    sb.Name,
		Namespace:      sb.Namespace,
		TenantName:     sb.Spec.TenantRef.Name,
		RuntimeType:    sb.Spec.Runtime,
		Image:          sb.Spec.Image,
		Command:        sb.Spec.Command,
		Args:           sb.Spec.Args,
		Resources:      sb.Spec.Resources,
		NetworkConfig:  sb.Spec.Network,
		StorageConfig:  sb.Spec.Storage,
		SecurityConfig: sb.Spec.Security,
		NodeName:       sb.Status.NodeName,
	}

	// Build environment variables
	if len(sb.Spec.Env) > 0 {
		spec.Env = make(map[string]string, len(sb.Spec.Env))
		for _, envVar := range sb.Spec.Env {
			spec.Env[envVar.Name] = envVar.Value
		}
	}

	// Build annotations
	if sb.Annotations != nil {
		spec.Annotations = make(map[string]string, len(sb.Annotations))
		for k, v := range sb.Annotations {
			spec.Annotations[k] = v
		}
	}

	return spec
}

// onSandboxAdd handles sandbox addition events.
func (sc *SandboxController) onSandboxAdd(obj interface{}) {
	metaObj, ok := obj.(metav1.Object)
	if !ok {
		return
	}
	key := metaObj.GetNamespace() + "/" + metaObj.GetName()
	sc.queue.Add(key)
}

// onSandboxUpdate handles sandbox update events.
func (sc *SandboxController) onSandboxUpdate(oldObj, newObj interface{}) {
	metaObj, ok := newObj.(metav1.Object)
	if !ok {
		return
	}
	key := metaObj.GetNamespace() + "/" + metaObj.GetName()
	sc.queue.Add(key)
}

// onSandboxDelete handles sandbox deletion events.
func (sc *SandboxController) onSandboxDelete(obj interface{}) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		return
	}
	sc.queue.Add(key)
}
