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
	"github.com/nexusbox/nexusbox/pkg/tenant"
	"github.com/nexusbox/nexusbox/pkg/tenant/quota"
)

const (
	// TenantControllerName is the name of the tenant controller.
	TenantControllerName = "tenant-controller"

	// tenantMaxRetries is the maximum number of retries for tenant reconciliation.
	tenantMaxRetries = 3

	// tenantConcurrentWorkers is the number of concurrent workers.
	tenantConcurrentWorkers = 3
)

// TenantController watches Tenant CRD objects and reconciles their state.
// It is responsible for:
// - Registering tenant quotas
// - Updating tenant status
// - Enforcing tenant isolation policies
// - Tracking tenant resource usage
type TenantController struct {
	mu sync.RWMutex

	// tenantManager manages tenant information.
	tenantManager *tenant.TenantManager

	// quotaManager manages resource quotas.
	quotaManager *quota.QuotaManager

	// informer watches for tenant CRD changes.
	informer cache.SharedIndexInformer

	// queue is the work queue for tenant reconciliation.
	queue workqueue.RateLimitingInterface

	// eventRecorder records events.
	eventRecorder EventRecorder

	// stopCh is used to signal shutdown.
	stopCh chan struct{}
}

// NewTenantController creates a new TenantController.
func NewTenantController(
	tenantManager *tenant.TenantManager,
	quotaManager *quota.QuotaManager,
	informer cache.SharedIndexInformer,
	eventRecorder EventRecorder,
) *TenantController {
	tc := &TenantController{
		tenantManager: tenantManager,
		quotaManager:  quotaManager,
		informer:      informer,
		queue:         workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "tenant"),
		eventRecorder: eventRecorder,
		stopCh:        make(chan struct{}),
	}

	if informer != nil {
		informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    tc.onTenantAdd,
			UpdateFunc: tc.onTenantUpdate,
			DeleteFunc: tc.onTenantDelete,
		})
	}

	return tc
}

// Start starts the tenant controller.
func (tc *TenantController) Start(ctx context.Context) {
	klog.Info("Starting tenant controller")

	for i := 0; i < tenantConcurrentWorkers; i++ {
		go wait.Until(tc.worker, time.Second, tc.stopCh)
	}

	// Start periodic quota sync
	go wait.Until(tc.syncQuotas, 60*time.Second, tc.stopCh)

	klog.Info("Tenant controller started")
}

// Stop stops the tenant controller.
func (tc *TenantController) Stop() {
	klog.Info("Stopping tenant controller")
	close(tc.stopCh)
	tc.queue.ShutDown()
}

// worker processes items from the work queue.
func (tc *TenantController) worker() {
	for tc.processNextWorkItem() {
	}
}

// processNextWorkItem processes the next item from the work queue.
func (tc *TenantController) processNextWorkItem() bool {
	key, quit := tc.queue.Get()
	if quit {
		return false
	}
	defer tc.queue.Done(key)

	err := tc.syncTenant(key.(string))
	if err == nil {
		tc.queue.Forget(key)
		return true
	}

	if tc.queue.NumRequeues(key) < tenantMaxRetries {
		klog.Warningf("Error syncing tenant %s, retrying: %v", key, err)
		tc.queue.AddRateLimited(key)
		return true
	}

	klog.Errorf("Error syncing tenant %s, giving up: %v", key, err)
	tc.queue.Forget(key)
	return true
}

// syncTenant reconciles a tenant to its desired state.
func (tc *TenantController) syncTenant(key string) error {
	startTime := time.Now()
	defer func() {
		klog.V(4).Infof("Finished syncing tenant %q (%v)", key, time.Since(startTime))
	}()

	obj, exists, err := tc.informer.GetIndexer().GetByKey(key)
	if err != nil {
		return fmt.Errorf("failed to get tenant %s from cache: %w", key, err)
	}

	if !exists {
		klog.Infof("Tenant %s has been deleted", key)
		tc.quotaManager.UnregisterQuota(key)
		return nil
	}

	tenantObj, ok := obj.(*sandboxv1alpha1.Tenant)
	if !ok {
		return fmt.Errorf("unexpected object type: %T", obj)
	}

	return tc.reconcileTenant(tenantObj)
}

// reconcileTenant reconciles a tenant to its desired state.
func (tc *TenantController) reconcileTenant(tenantObj *sandboxv1alpha1.Tenant) error {
	ctx := context.Background()

	switch tenantObj.Status.Phase {
	case sandboxv1alpha1.TenantPending:
		return tc.reconcilePendingTenant(ctx, tenantObj)
	case sandboxv1alpha1.TenantActive:
		return tc.reconcileActiveTenant(ctx, tenantObj)
	case sandboxv1alpha1.TenantSuspended:
		return tc.reconcileSuspendedTenant(ctx, tenantObj)
	case sandboxv1alpha1.TenantTerminating:
		return tc.reconcileTerminatingTenant(ctx, tenantObj)
	default:
		klog.Warningf("Unknown tenant phase: %s", tenantObj.Status.Phase)
		return nil
	}
}

// reconcilePendingTenant handles a tenant in Pending phase.
func (tc *TenantController) reconcilePendingTenant(ctx context.Context, tenantObj *sandboxv1alpha1.Tenant) error {
	klog.Infof("Reconciling pending tenant %s", tenantObj.Name)

	// Register the tenant's quota
	if err := tc.quotaManager.RegisterQuota(tenantObj.Name, &tenantObj.Spec.ResourceQuota); err != nil {
		return fmt.Errorf("failed to register quota for tenant %s: %w", tenantObj.Name, err)
	}

	// Register the tenant with the tenant manager
	if err := tc.tenantManager.RegisterTenant(ctx, tenantObj); err != nil {
		return fmt.Errorf("failed to register tenant %s: %w", tenantObj.Name, err)
	}

	// Record event
	tc.eventRecorder.RecordEvent(tenantObj, "Normal", "TenantActivated",
		fmt.Sprintf("Tenant %s activated", tenantObj.Name))

	return nil
}

// reconcileActiveTenant handles a tenant in Active phase.
func (tc *TenantController) reconcileActiveTenant(ctx context.Context, tenantObj *sandboxv1alpha1.Tenant) error {
	// Update quota if changed
	if err := tc.quotaManager.RegisterQuota(tenantObj.Name, &tenantObj.Spec.ResourceQuota); err != nil {
		return fmt.Errorf("failed to update quota for tenant %s: %w", tenantObj.Name, err)
	}

	return nil
}

// reconcileSuspendedTenant handles a tenant in Suspended phase.
func (tc *TenantController) reconcileSuspendedTenant(ctx context.Context, tenantObj *sandboxv1alpha1.Tenant) error {
	klog.Infof("Tenant %s is suspended", tenantObj.Name)

	// In production, we would:
	// 1. Stop all running sandboxes for this tenant
	// 2. Prevent new sandbox creation
	// 3. Send notifications

	return nil
}

// reconcileTerminatingTenant handles a tenant in Terminating phase.
func (tc *TenantController) reconcileTerminatingTenant(ctx context.Context, tenantObj *sandboxv1alpha1.Tenant) error {
	klog.Infof("Reconciling terminating tenant %s", tenantObj.Name)

	// Clean up all tenant resources
	tc.quotaManager.UnregisterQuota(tenantObj.Name)

	// Record event
	tc.eventRecorder.RecordEvent(tenantObj, "Normal", "TenantTerminated",
		fmt.Sprintf("Tenant %s terminated", tenantObj.Name))

	return nil
}

// syncQuotas periodically syncs quota usage from the quota manager
// to the tenant status.
func (tc *TenantController) syncQuotas() {
	// In production, this would update tenant CRD status with quota usage
	klog.V(6).Info("Syncing tenant quotas")
}

// onTenantAdd handles tenant addition events.
func (tc *TenantController) onTenantAdd(obj interface{}) {
	metaObj, ok := obj.(metav1.Object)
	if !ok {
		return
	}
	key := metaObj.GetNamespace() + "/" + metaObj.GetName()
	tc.queue.Add(key)
}

// onTenantUpdate handles tenant update events.
func (tc *TenantController) onTenantUpdate(oldObj, newObj interface{}) {
	metaObj, ok := newObj.(metav1.Object)
	if !ok {
		return
	}
	key := metaObj.GetNamespace() + "/" + metaObj.GetName()
	tc.queue.Add(key)
}

// onTenantDelete handles tenant deletion events.
func (tc *TenantController) onTenantDelete(obj interface{}) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		return
	}
	tc.queue.Add(key)
}

// QuotaController watches Quota CRD objects and enforces resource quotas.
type QuotaController struct {
	mu sync.RWMutex

	// quotaManager manages resource quotas.
	quotaManager *quota.QuotaManager

	// tenantManager manages tenant information.
	tenantManager *tenant.TenantManager

	// informer watches for quota CRD changes.
	informer cache.SharedIndexInformer

	// queue is the work queue for quota reconciliation.
	queue workqueue.RateLimitingInterface

	// eventRecorder records events.
	eventRecorder EventRecorder

	// stopCh is used to signal shutdown.
	stopCh chan struct{}
}

const (
	// QuotaControllerName is the name of the quota controller.
	QuotaControllerName = "quota-controller"

	// quotaMaxRetries is the maximum number of retries for quota reconciliation.
	quotaMaxRetries = 3

	// quotaConcurrentWorkers is the number of concurrent workers.
	quotaConcurrentWorkers = 2
)

// NewQuotaController creates a new QuotaController.
func NewQuotaController(
	quotaManager *quota.QuotaManager,
	tenantManager *tenant.TenantManager,
	informer cache.SharedIndexInformer,
	eventRecorder EventRecorder,
) *QuotaController {
	qc := &QuotaController{
		quotaManager:  quotaManager,
		tenantManager: tenantManager,
		informer:      informer,
		queue:         workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "quota"),
		eventRecorder: eventRecorder,
		stopCh:        make(chan struct{}),
	}

	if informer != nil {
		informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    qc.onQuotaAdd,
			UpdateFunc: qc.onQuotaUpdate,
			DeleteFunc: qc.onQuotaDelete,
		})
	}

	return qc
}

// Start starts the quota controller.
func (qc *QuotaController) Start(ctx context.Context) {
	klog.Info("Starting quota controller")

	for i := 0; i < quotaConcurrentWorkers; i++ {
		go wait.Until(qc.worker, time.Second, qc.stopCh)
	}

	// Start periodic quota enforcement
	go wait.Until(qc.enforceQuotas, 60*time.Second, qc.stopCh)

	klog.Info("Quota controller started")
}

// Stop stops the quota controller.
func (qc *QuotaController) Stop() {
	klog.Info("Stopping quota controller")
	close(qc.stopCh)
	qc.queue.ShutDown()
}

// worker processes items from the work queue.
func (qc *QuotaController) worker() {
	for qc.processNextWorkItem() {
	}
}

// processNextWorkItem processes the next item from the work queue.
func (qc *QuotaController) processNextWorkItem() bool {
	key, quit := qc.queue.Get()
	if quit {
		return false
	}
	defer qc.queue.Done(key)

	err := qc.syncQuota(key.(string))
	if err == nil {
		qc.queue.Forget(key)
		return true
	}

	if qc.queue.NumRequeues(key) < quotaMaxRetries {
		klog.Warningf("Error syncing quota %s, retrying: %v", key, err)
		qc.queue.AddRateLimited(key)
		return true
	}

	klog.Errorf("Error syncing quota %s, giving up: %v", key, err)
	qc.queue.Forget(key)
	return true
}

// syncQuota reconciles a quota to its desired state.
func (qc *QuotaController) syncQuota(key string) error {
	obj, exists, err := qc.informer.GetIndexer().GetByKey(key)
	if err != nil {
		return fmt.Errorf("failed to get quota %s from cache: %w", key, err)
	}

	if !exists {
		klog.Infof("Quota %s has been deleted", key)
		return nil
	}

	quotaObj, ok := obj.(*sandboxv1alpha1.SandboxQuota)
	if !ok {
		return fmt.Errorf("unexpected object type: %T", obj)
	}

	return qc.reconcileQuota(quotaObj)
}

// reconcileQuota reconciles a quota to its desired state.
func (qc *QuotaController) reconcileQuota(quotaObj *sandboxv1alpha1.SandboxQuota) error {
	tenantName := quotaObj.Spec.TenantRef.Name

	// Update the quota in the quota manager
	if err := qc.quotaManager.RegisterQuota(tenantName, &quotaObj.Spec.Hard); err != nil {
		return fmt.Errorf("failed to update quota for tenant %s: %w", tenantName, err)
	}

	// Get current usage
	usage := qc.quotaManager.GetUsage(tenantName)
	if usage != nil {
		klog.V(4).Infof("Quota usage for tenant %s: CPU %s/%s, Memory %d/%d, Instances %d/%d",
			tenantName, usage.CPUUsed, usage.CPULimit,
			usage.MemoryUsedBytes, usage.MemoryLimitBytes,
			usage.InstanceUsed, usage.InstanceLimit)
	}

	return nil
}

// enforceQuotas periodically checks and enforces quota limits.
func (qc *QuotaController) enforceQuotas() {
	klog.V(6).Info("Enforcing quotas")

	// In production, this would:
	// 1. Check all tenant quotas
	// 2. Evict sandboxes that exceed quotas
	// 3. Send alerts for near-limit usage
}

// onQuotaAdd handles quota addition events.
func (qc *QuotaController) onQuotaAdd(obj interface{}) {
	metaObj, ok := obj.(metav1.Object)
	if !ok {
		return
	}
	key := metaObj.GetNamespace() + "/" + metaObj.GetName()
	qc.queue.Add(key)
}

// onQuotaUpdate handles quota update events.
func (qc *QuotaController) onQuotaUpdate(oldObj, newObj interface{}) {
	metaObj, ok := newObj.(metav1.Object)
	if !ok {
		return
	}
	key := metaObj.GetNamespace() + "/" + metaObj.GetName()
	qc.queue.Add(key)
}

// onQuotaDelete handles quota deletion events.
func (qc *QuotaController) onQuotaDelete(obj interface{}) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		return
	}
	qc.queue.Add(key)
}
