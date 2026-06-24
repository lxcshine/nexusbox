package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/klog/v2"
)

// GracefulShutdown wraps an HTTP server with graceful shutdown support.
type GracefulShutdown struct {
	server   *http.Server
	timeout  time.Duration
	stopCh   chan struct{}
}

// NewGracefulShutdown creates a new graceful shutdown wrapper.
func NewGracefulShutdown(server *http.Server, timeout time.Duration) *GracefulShutdown {
	return &GracefulShutdown{
		server:  server,
		timeout: timeout,
		stopCh:  make(chan struct{}),
	}
}

// ListenAndServe starts the server and handles graceful shutdown.
func (gs *GracefulShutdown) ListenAndServe() error {
	errCh := make(chan error, 1)
	go func() {
		klog.Infof("Server listening on %s", gs.server.Addr)
		if err := gs.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	// Wait for interrupt signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-sigCh:
		klog.Infof("Received signal %v, shutting down gracefully...", sig)
		return gs.Shutdown()
	case <-gs.stopCh:
		klog.Info("Stop channel closed, shutting down gracefully...")
		return gs.Shutdown()
	}
}

// Shutdown gracefully shuts down the server.
func (gs *GracefulShutdown) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), gs.timeout)
	defer cancel()

	klog.Infof("Shutting down server (timeout: %v)...", gs.timeout)

	if err := gs.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("server shutdown failed: %w", err)
	}

	klog.Info("Server shutdown complete")
	return nil
}

// Stop signals the server to stop.
func (gs *GracefulShutdown) Stop() {
	close(gs.stopCh)
}

// WaitForShutdown waits for a context to be done, then triggers graceful shutdown.
func WaitForShutdown(ctx context.Context, shutdownFunc func()) {
	<-ctx.Done()
	klog.Info("Context cancelled, initiating shutdown...")
	shutdownFunc()
}

// SetupSignalHandler registers for SIGTERM and SIGINT and returns a channel
// that is closed on receipt of either signal.
func SetupSignalHandler() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		<-sigCh
		cancel()
		<-sigCh
		os.Exit(1) // Second signal exits immediately
	}()

	return ctx
}
