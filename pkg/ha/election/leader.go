package election

import (
	"context"
	"fmt"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
	"k8s.io/klog/v2"
)

// LeaderElection implements leader election using etcd.
type LeaderElection struct {
	client   *clientv3.Client
	session  *concurrency.Session
	election *concurrency.Election
	identity string
	prefix   string
	leaderCh chan bool
	stopCh   chan struct{}
}

// LeaderElectionConfig holds configuration for leader election.
type LeaderElectionConfig struct {
	// Client is the etcd client.
	Client *clientv3.Client
	// Identity is the unique identifier for this instance.
	Identity string
	// Prefix is the etcd key prefix for the election.
	Prefix string
	// LeaseDuration is the duration of the leader lease.
	LeaseDuration time.Duration
	// RenewDeadline is the deadline for renewing the lease.
	RenewDeadline time.Duration
	// RetryPeriod is the period between retry attempts.
	RetryPeriod time.Duration
	// OnStartedLeading is called when this instance becomes leader.
	OnStartedLeading func(ctx context.Context)
	// OnStoppedLeading is called when this instance stops being leader.
	OnStoppedLeading func()
	// OnNewLeader is called when a new leader is elected.
	OnNewLeader func(identity string)
}

// NewLeaderElection creates a new leader election instance.
func NewLeaderElection(config *LeaderElectionConfig) (*LeaderElection, error) {
	if config.Prefix == "" {
		config.Prefix = "/nexusbox/leader"
	}
	if config.LeaseDuration == 0 {
		config.LeaseDuration = 15 * time.Second
	}

	session, err := concurrency.NewSession(config.Client, concurrency.WithTTL(int(config.LeaseDuration.Seconds())))
	if err != nil {
		return nil, fmt.Errorf("failed to create etcd session: %w", err)
	}

	election := concurrency.NewElection(session, config.Prefix)

	return &LeaderElection{
		client:   config.Client,
		session:  session,
		election: election,
		identity: config.Identity,
		prefix:   config.Prefix,
		leaderCh: make(chan bool, 1),
		stopCh:   make(chan struct{}),
	}, nil
}

// Run starts the leader election loop.
func (le *LeaderElection) Run(ctx context.Context, config *LeaderElectionConfig) {
	for {
		select {
		case <-le.stopCh:
			klog.Infof("Leader election stopped for %s", le.identity)
			if config.OnStoppedLeading != nil {
				config.OnStoppedLeading()
			}
			return
		case <-ctx.Done():
			klog.Infof("Leader election context cancelled for %s", le.identity)
			if config.OnStoppedLeading != nil {
				config.OnStoppedLeading()
			}
			return
		default:
		}

		// Campaign for leadership
		klog.Infof("Instance %s campaigning for leadership", le.identity)
		if err := le.election.Campaign(ctx, le.identity); err != nil {
			klog.Warningf("Campaign failed for %s: %v", le.identity, err)
			time.Sleep(config.RetryPeriod)
			continue
		}

		// We are now the leader
		klog.Infof("Instance %s elected as leader", le.identity)
		le.leaderCh <- true

		if config.OnNewLeader != nil {
			config.OnNewLeader(le.identity)
		}

		if config.OnStartedLeading != nil {
			config.OnStartedLeading(ctx)
		}

		// Observe for leadership loss
		select {
		case <-ctx.Done():
			if config.OnStoppedLeading != nil {
				config.OnStoppedLeading()
			}
			return
		case <-le.session.Done():
			klog.Warningf("Leader session lost for %s", le.identity)
			if config.OnStoppedLeading != nil {
				config.OnStoppedLeading()
			}
			// Re-create session and try again
			session, err := concurrency.NewSession(le.client, concurrency.WithTTL(int(config.LeaseDuration.Seconds())))
			if err != nil {
				klog.Errorf("Failed to recreate session: %v", err)
				return
			}
			le.session = session
			le.election = concurrency.NewElection(session, le.prefix)
		}
	}
}

// IsLeader returns true if this instance is the current leader.
func (le *LeaderElection) IsLeader() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := le.election.Leader(ctx)
	if err != nil {
		return false
	}
	if len(resp.Kvs) == 0 {
		return false
	}
	return string(resp.Kvs[0].Value) == le.identity
}

// LeaderCh returns a channel that receives true when this instance becomes leader.
func (le *LeaderElection) LeaderCh() <-chan bool {
	return le.leaderCh
}

// Stop stops the leader election.
func (le *LeaderElection) Stop() {
	close(le.stopCh)
	le.election.Resign(context.Background())
	le.session.Close()
}

// GetLeaderIdentity returns the identity of the current leader.
func (le *LeaderElection) GetLeaderIdentity(ctx context.Context) (string, error) {
	resp, err := le.election.Leader(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get leader: %w", err)
	}
	if len(resp.Kvs) == 0 {
		return "", fmt.Errorf("no leader found")
	}
	return string(resp.Kvs[0].Value), nil
}
