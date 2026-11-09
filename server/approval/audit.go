package approval

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/webcenter-fr/eino-ext/server/approval/store"
)

// AuditEvent is the approval service's independent audit record. It is kept
// separately from the MCP server's safety.AuditEvent.
type AuditEvent struct {
	Timestamp    time.Time `json:"timestamp"`
	ApprovalID   string    `json:"approvalId"`
	ServerName   string    `json:"serverName"`
	ToolName     string    `json:"toolName"`
	Instance     string    `json:"instance,omitempty"`
	RequesterSub string    `json:"requesterSub,omitempty"`
	ApproverSub  string    `json:"approverSub,omitempty"`
	Decision     string    `json:"decision,omitempty"`
	Policy       string    `json:"policy,omitempty"`
	ArgsHash     string    `json:"argsHash,omitempty"`
	State        string    `json:"state,omitempty"`
	JTI          string    `json:"jti,omitempty"`
	Error        string    `json:"error,omitempty"`
}

// AuditSink receives approval audit records. Implementations must be safe for
// concurrent use.
type AuditSink interface {
	Write(ctx context.Context, event AuditEvent) error
}

// AuditSinkFunc adapts a function to AuditSink.
type AuditSinkFunc func(ctx context.Context, event AuditEvent) error

// Write implements AuditSink.
func (f AuditSinkFunc) Write(ctx context.Context, event AuditEvent) error {
	return f(ctx, event)
}

// LogAuditSink writes audit records as structured logs.
type LogAuditSink struct{}

// Write implements AuditSink.
func (s *LogAuditSink) Write(_ context.Context, event AuditEvent) error {
	logrus.WithFields(logrus.Fields{
		"approval_id":    event.ApprovalID,
		"approval_srv":   event.ServerName,
		"approval_tool":  event.ToolName,
		"approval_state": event.State,
		"approval_sub":   event.ApproverSub,
		"approval_jti":   event.JTI,
	}).Info("approval audit event")
	return nil
}

// Notifier is notified when a new approval is registered. The default is a
// no-op; operators can plug email/chat/pager integrations.
type Notifier interface {
	Notify(ctx context.Context, rec *store.Record) error
}

// NoopNotifier does nothing.
type NoopNotifier struct{}

// Notify implements Notifier.
func (NoopNotifier) Notify(context.Context, *store.Record) error { return nil }
