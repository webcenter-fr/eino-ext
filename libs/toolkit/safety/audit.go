package safety

import (
	"context"
	"encoding/json"
	"time"

	"github.com/sirupsen/logrus"
)

// AuditEvent represents a single tool invocation audit record.
// It captures the complete lifecycle of a tool call: who called what, with
// which arguments, what the result was, and whether policies passed.
type AuditEvent struct {
	Timestamp  time.Time         `json:"timestamp"`
	ToolName   string            `json:"toolName"`
	CallID     string            `json:"callID"`
	Phase      Phase             `json:"phase"`
	Operation  OperationType     `json:"operation,omitempty"`
	Arguments  json.RawMessage   `json:"arguments"`
	Result     string            `json:"result,omitempty"`
	Error      string            `json:"error,omitempty"`
	PolicyPass bool              `json:"policyPass"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// AuditSink is the interface consumers implement to receive audit events.
// Implementations must be safe for concurrent use.
type AuditSink interface {
	Write(ctx context.Context, event AuditEvent) error
}

// AuditSinkFunc is a function adapter for AuditSink.
type AuditSinkFunc func(ctx context.Context, event AuditEvent) error

// Write implements AuditSink by delegating to the underlying function.
func (f AuditSinkFunc) Write(ctx context.Context, event AuditEvent) error {
	return f(ctx, event)
}

// ChannelSink sends audit events to a buffered channel for streaming consumers.
// Callers should read from Events() in a goroutine and call Close() when done.
type ChannelSink struct {
	ch chan AuditEvent
}

// NewChannelSink creates a ChannelSink with the given buffer size.
func NewChannelSink(bufferSize int) *ChannelSink {
	if bufferSize <= 0 {
		bufferSize = 100
	}
	return &ChannelSink{ch: make(chan AuditEvent, bufferSize)}
}

// Write sends the audit event to the internal channel.
// It is non-blocking; if the channel is full the event is dropped (fire-and-forget).
func (s *ChannelSink) Write(_ context.Context, event AuditEvent) error {
	select {
	case s.ch <- event:
	default:
		// Channel full — drop event rather than blocking the tool call.
		logrus.Warn("audit ChannelSink buffer full, dropping event")
	}
	return nil
}

// Events returns a receive-only channel for consuming audit events.
func (s *ChannelSink) Events() <-chan AuditEvent {
	return s.ch
}

// Close closes the event channel. After Close, no more events will be written.
func (s *ChannelSink) Close() {
	close(s.ch)
}

// LogSink writes audit events as structured log entries via logrus.
// It is the default audit sink when none is configured.
type LogSink struct{}

// Write logs the audit event at info level as structured JSON.
func (s *LogSink) Write(_ context.Context, event AuditEvent) error {
	logrus.WithFields(logrus.Fields{
		"audit_tool":   event.ToolName,
		"audit_callid": event.CallID,
		"audit_phase":  event.Phase,
		"audit_op":     event.Operation,
		"audit_policy": event.PolicyPass,
	}).Info("audit event")
	return nil
}
