package event

import (
	"fmt"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// Event represents a system event.
type Event struct {
	// Type is the type of event (Normal, Warning).
	Type string

	// Reason is the reason for the event.
	Reason string

	// Message is the human-readable message.
	Message string

	// Source is the source of the event.
	Source string

	// InvolvedObject is the object involved in the event.
	InvolvedObject ObjectReference

	// Timestamp is when the event occurred.
	Timestamp time.Time

	// Count is the number of times this event has occurred.
	Count int32

	// FirstTimestamp is the first time this event occurred.
	FirstTimestamp time.Time

	// LastTimestamp is the last time this event occurred.
	LastTimestamp time.Time
}

// ObjectReference represents a reference to an object.
type ObjectReference struct {
	Kind      string
	Name      string
	Namespace string
	UID       string
}

// EventRecorder records events for the NexusBox system.
// It provides a centralized way to record and query events
// across all system components.
type EventRecorder struct {
	mu sync.RWMutex

	// events stores all recorded events.
	events []*Event

	// maxEvents is the maximum number of events to retain.
	maxEvents int

	// eventHandlers are registered event handlers.
	eventHandlers []EventHandler

	// counters tracks event counts by reason.
	counters map[string]int64
}

// EventHandler is a callback for handling events.
type EventHandler func(event *Event)

// NewEventRecorder creates a new EventRecorder.
func NewEventRecorder(maxEvents int) *EventRecorder {
	if maxEvents <= 0 {
		maxEvents = 10000
	}

	return &EventRecorder{
		events:    make([]*Event, 0, maxEvents),
		maxEvents: maxEvents,
		counters:  make(map[string]int64),
	}
}

// RecordEvent records a new event.
func (er *EventRecorder) RecordEvent(eventType, reason, message string, obj ObjectReference) {
	er.mu.Lock()
	defer er.mu.Unlock()

	now := time.Now()

	// Check for duplicate events (same reason, same object)
	var existingEvent *Event
	for _, e := range er.events {
		if e.Reason == reason && e.InvolvedObject == obj {
			existingEvent = e
			break
		}
	}

	if existingEvent != nil {
		// Update existing event
		existingEvent.Count++
		existingEvent.LastTimestamp = now
		existingEvent.Message = message
	} else {
		// Create new event
		event := &Event{
			Type:           eventType,
			Reason:         reason,
			Message:        message,
			Source:         "nexusbox-manager",
			InvolvedObject: obj,
			Timestamp:      now,
			Count:          1,
			FirstTimestamp: now,
			LastTimestamp:  now,
		}

		er.events = append(er.events, event)

		// Trim events if over limit
		if len(er.events) > er.maxEvents {
			er.events = er.events[len(er.events)-er.maxEvents:]
		}
	}

	// Update counter
	er.counters[reason]++

	// Notify handlers
	for _, handler := range er.eventHandlers {
		handler(er.events[len(er.events)-1])
	}

	klog.V(4).Infof("Event: [%s] %s - %s (object: %s/%s)",
		eventType, reason, message, obj.Namespace, obj.Name)
}

// RecordSandboxEvent records an event for a sandbox.
func (er *EventRecorder) RecordSandboxEvent(sandboxName, namespace, eventType, reason, message string) {
	er.RecordEvent(eventType, reason, message, ObjectReference{
		Kind:      "Sandbox",
		Name:      sandboxName,
		Namespace: namespace,
	})
}

// RecordTenantEvent records an event for a tenant.
func (er *EventRecorder) RecordTenantEvent(tenantName, eventType, reason, message string) {
	er.RecordEvent(eventType, reason, message, ObjectReference{
		Kind: "Tenant",
		Name: tenantName,
	})
}

// RecordNodeEvent records an event for a node.
func (er *EventRecorder) RecordNodeEvent(nodeName, eventType, reason, message string) {
	er.RecordEvent(eventType, reason, message, ObjectReference{
		Kind: "Node",
		Name: nodeName,
	})
}

// GetEvents retrieves events matching the given filters.
func (er *EventRecorder) GetEvents(filter *EventFilter) []*Event {
	er.mu.RLock()
	defer er.mu.RUnlock()

	var result []*Event
	for _, event := range er.events {
		if filter != nil && !filter.Match(event) {
			continue
		}
		result = append(result, event)
	}

	return result
}

// GetEventsForObject retrieves events for a specific object.
func (er *EventRecorder) GetEventsForObject(kind, namespace, name string) []*Event {
	er.mu.RLock()
	defer er.mu.RUnlock()

	var result []*Event
	for _, event := range er.events {
		if event.InvolvedObject.Kind == kind &&
			event.InvolvedObject.Namespace == namespace &&
			event.InvolvedObject.Name == name {
			result = append(result, event)
		}
	}

	return result
}

// GetEventCount returns the count of events by reason.
func (er *EventRecorder) GetEventCount(reason string) int64 {
	er.mu.RLock()
	defer er.mu.RUnlock()
	return er.counters[reason]
}

// RegisterHandler registers an event handler.
func (er *EventRecorder) RegisterHandler(handler EventHandler) {
	er.mu.Lock()
	defer er.mu.Unlock()
	er.eventHandlers = append(er.eventHandlers, handler)
}

// EventFilter filters events based on criteria.
type EventFilter struct {
	// Type filters by event type.
	Type string

	// Reason filters by event reason.
	Reason string

	// Source filters by event source.
	Source string

	// Namespace filters by namespace.
	Namespace string

	// Kind filters by involved object kind.
	Kind string

	// Since filters events after this time.
	Since time.Time

	// Until filters events before this time.
	Until time.Time
}

// Match checks if an event matches the filter.
func (f *EventFilter) Match(event *Event) bool {
	if f.Type != "" && event.Type != f.Type {
		return false
	}
	if f.Reason != "" && event.Reason != f.Reason {
		return false
	}
	if f.Source != "" && event.Source != f.Source {
		return false
	}
	if f.Namespace != "" && event.InvolvedObject.Namespace != f.Namespace {
		return false
	}
	if f.Kind != "" && event.InvolvedObject.Kind != f.Kind {
		return false
	}
	if !f.Since.IsZero() && event.Timestamp.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && event.Timestamp.After(f.Until) {
		return false
	}
	return true
}

// EventReason constants for common event reasons.
const (
	// Sandbox events
	SandboxCreated          = "SandboxCreated"
	SandboxScheduled        = "SandboxScheduled"
	SandboxStarting         = "SandboxStarting"
	SandboxStarted          = "SandboxStarted"
	SandboxStopping         = "SandboxStopping"
	SandboxStopped          = "SandboxStopped"
	SandboxPausing          = "SandboxPausing"
	SandboxPaused           = "SandboxPaused"
	SandboxResuming         = "SandboxResuming"
	SandboxResumed          = "SandboxResumed"
	SandboxFailed           = "SandboxFailed"
	SandboxEvicted          = "SandboxEvicted"
	SandboxTerminating      = "SandboxTerminating"
	SandboxTerminated       = "SandboxTerminated"
	SandboxTimeout          = "SandboxTimeout"
	SandboxRetryExhausted   = "RetryExhausted"
	SandboxQuotaExceeded    = "QuotaExceeded"
	SandboxRateLimited      = "RateLimited"

	// Scheduling events
	SchedulingSucceeded     = "SchedulingSucceeded"
	SchedulingFailed        = "SchedulingFailed"
	SchedulingUnschedulable = "SchedulingUnschedulable"
	SchedulingPreempted     = "SchedulingPreempted"

	// Tenant events
	TenantCreated           = "TenantCreated"
	TenantUpdated           = "TenantUpdated"
	TenantDeleted           = "TenantDeleted"
	TenantSuspended         = "TenantSuspended"
	TenantActivated         = "TenantActivated"
	TenantQuotaWarning      = "TenantQuotaWarning"

	// Node events
	NodeReady               = "NodeReady"
	NodeNotReady            = "NodeNotReady"
	NodeAdded               = "NodeAdded"
	NodeRemoved             = "NodeRemoved"

	// Runtime events
	RuntimeCreated          = "RuntimeCreated"
	RuntimeStarted          = "RuntimeStarted"
	RuntimeStopped          = "RuntimeStopped"
	RuntimeFailed           = "RuntimeFailed"
	RuntimeHealthCheck      = "RuntimeHealthCheck"

	// Pool events
	PoolExpanded            = "PoolExpanded"
	PoolShrunk              = "PoolShrunk"
	PoolItemReused          = "PoolItemReused"
)

// FormatEvent formats an event for display.
func FormatEvent(event *Event) string {
	return fmt.Sprintf("[%s] %s %s/%s: %s (count: %d)",
		event.Type,
		event.Reason,
		event.InvolvedObject.Kind,
		event.InvolvedObject.Name,
		event.Message,
		event.Count,
	)
}
