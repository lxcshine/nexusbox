package image

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/containerd/containerd"
	"k8s.io/klog/v2"
)

// ImageManager manages container images for sandboxes.
type ImageManager struct {
	mu      sync.RWMutex
	client  *containerd.Client
	cache   map[string]*ImageInfo
	pullers map[string]*PullTask
}

// ImageInfo contains information about a cached image.
type ImageInfo struct {
	Name       string
	Digest     string
	Size       int64
	PulledAt   time.Time
	LastUsedAt time.Time
}

// PullTask represents an in-progress image pull.
type PullTask struct {
	Name      string
	StartedAt time.Time
	Done      chan struct{}
	Err       error
}

// NewImageManager creates a new image manager.
func NewImageManager(client *containerd.Client) *ImageManager {
	return &ImageManager{
		client:  client,
		cache:   make(map[string]*ImageInfo),
		pullers: make(map[string]*PullTask),
	}
}

// PullImage pulls an image with deduplication.
func (im *ImageManager) PullImage(ctx context.Context, ref string) error {
	im.mu.Lock()
	// Check if already cached
	if info, ok := im.cache[ref]; ok {
		im.mu.Unlock()
		klog.V(4).Infof("Image %s already cached (pulled at %s)", ref, info.PulledAt)
		return nil
	}

	// Check if pull is in progress
	if task, ok := im.pullers[ref]; ok {
		im.mu.Unlock()
		klog.V(4).Infof("Image %s pull already in progress, waiting...", ref)
		<-task.Done
		return task.Err
	}

	// Start new pull
	task := &PullTask{
		Name:      ref,
		StartedAt: time.Now(),
		Done:      make(chan struct{}),
	}
	im.pullers[ref] = task
	im.mu.Unlock()

	// Perform the pull
	klog.Infof("Pulling image %s...", ref)
	_, err := im.client.Pull(ctx, ref, containerd.WithPullUnpack)

	im.mu.Lock()
	defer im.mu.Unlock()

	if err != nil {
		task.Err = fmt.Errorf("failed to pull image %s: %w", ref, err)
		klog.Errorf("Failed to pull image %s: %v", ref, err)
	} else {
		im.cache[ref] = &ImageInfo{
			Name:       ref,
			PulledAt:   time.Now(),
			LastUsedAt: time.Now(),
		}
		klog.Infof("Successfully pulled image %s", ref)
	}

	delete(im.pullers, ref)
	close(task.Done)
	return task.Err
}

// GetImage returns image info from cache.
func (im *ImageManager) GetImage(ref string) (*ImageInfo, bool) {
	im.mu.RLock()
	defer im.mu.RUnlock()
	info, ok := im.cache[ref]
	if ok {
		info.LastUsedAt = time.Now()
	}
	return info, ok
}

// ListImages lists all cached images.
func (im *ImageManager) ListImages() []*ImageInfo {
	im.mu.RLock()
	defer im.mu.RUnlock()
	var result []*ImageInfo
	for _, info := range im.cache {
		result = append(result, info)
	}
	return result
}

// RemoveImage removes an image from cache.
func (im *ImageManager) RemoveImage(ctx context.Context, ref string) error {
	im.mu.Lock()
	defer im.mu.Unlock()

	if err := im.client.ImageService().Delete(ctx, ref); err != nil {
		return fmt.Errorf("failed to delete image %s: %w", ref, err)
	}
	delete(im.cache, ref)
	klog.Infof("Removed image %s", ref)
	return nil
}

// PruneImages removes unused images.
func (im *ImageManager) PruneImages(ctx context.Context, maxAge time.Duration) (int, error) {
	im.mu.Lock()
	defer im.mu.Unlock()

	now := time.Now()
	pruned := 0
	for ref, info := range im.cache {
		if now.Sub(info.LastUsedAt) > maxAge {
			if err := im.client.ImageService().Delete(ctx, ref); err != nil {
				klog.Warningf("Failed to prune image %s: %v", ref, err)
				continue
			}
			delete(im.cache, ref)
			pruned++
			klog.Infof("Pruned unused image %s (last used %v ago)", ref, now.Sub(info.LastUsedAt))
		}
	}
	return pruned, nil
}

// PrePullImages pre-pulls a list of images (for pool warm-up).
func (im *ImageManager) PrePullImages(ctx context.Context, refs []string) error {
	var firstErr error
	for _, ref := range refs {
		if err := im.PullImage(ctx, ref); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
