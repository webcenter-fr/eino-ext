// Package store defines the approval record model and the ApprovalStore
// interface shared by every approval backend.
package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"time"

	"emperror.dev/errors"
)

// State is the lifecycle state of an approval record.
type State string

// Known approval states.
const (
	StatePending  State = "pending"
	StateApproved State = "approved"
	StateRejected State = "rejected"
	StateExpired  State = "expired"
)

// Decision values accepted by Decide.
const (
	DecisionApprove = "approve"
	DecisionReject  = "reject"
)

// DefaultTTL is the default lifetime of a pending approval record. It is
// deliberately longer than the MCP client timeout so a decision survives a
// client that gave up polling.
const DefaultTTL = 15 * time.Minute

// DefaultRetention is how long a decided or expired record is kept after its
// expiry before GC removes it.
const DefaultRetention = time.Hour

// Requester identifies the human or agent that requested the write.
type Requester struct {
	Sub    string   `json:"sub"`
	User   string   `json:"user,omitempty"`
	Groups []string `json:"groups,omitempty"`
}

// Record is a single approval request.
type Record struct {
	ID             string          `json:"id"`
	ServerName     string          `json:"serverName"`
	ToolName       string          `json:"toolName"`
	Instance       string          `json:"instance,omitempty"`
	Operation      string          `json:"operation,omitempty"`
	Args           json.RawMessage `json:"args"`
	ArgsHash       string          `json:"argsHash"`
	Preview        string          `json:"preview,omitempty"`
	PreviewWarning string          `json:"previewWarning,omitempty"`
	Requester      Requester       `json:"requester"`
	State          State           `json:"state"`
	Policy         string          `json:"policy,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
	ExpiresAt      time.Time       `json:"expiresAt"`
	DecidedAt      *time.Time      `json:"decidedAt,omitempty"`
	ApproverSub    string          `json:"approverSub,omitempty"`
	ApproverUser   string          `json:"approverUser,omitempty"`
	Decision       string          `json:"decision,omitempty"`
	ReceiptJTI     string          `json:"receiptJti,omitempty"`
}

// Decision is the approver's decision.
type Decision struct {
	Decision     string
	ApproverSub  string
	ApproverUser string
	Policy       string
}

// ListFilter narrows ListPending. Zero values mean "no filter".
type ListFilter struct {
	ServerName string
	Instance   string
	ToolName   string
	Limit      int
}

// ApprovalStore persists approval records. Implementations must be safe for
// concurrent use.
type ApprovalStore interface {
	// Create stores a new record. The implementation fills ID, CreatedAt and
	// ExpiresAt when they are zero.
	Create(ctx context.Context, rec *Record) error
	// Get returns a record, transitioning a pending record past its expiry to
	// StateExpired.
	Get(ctx context.Context, id string) (*Record, error)
	// ListPending returns pending records matching the filter, oldest first.
	ListPending(ctx context.Context, filter ListFilter) ([]*Record, error)
	// Decide records an approve/reject decision. It fails with
	// ErrAlreadyDecided when the record is no longer pending.
	Decide(ctx context.Context, id string, d Decision) (*Record, error)
	// WaitDecision blocks until the record is decided, expires, wait elapses,
	// or ctx is cancelled. A wait of zero returns immediately.
	WaitDecision(ctx context.Context, id string, wait time.Duration) (*Record, error)
	// GC expires overdue pending records and deletes records past their
	// retention window. It returns the number of deleted records.
	GC(ctx context.Context, now time.Time) (int, error)
}

// Errors returned by stores.
var (
	ErrNotFound        = errors.New("approval record not found")
	ErrAlreadyDecided  = errors.New("approval record already decided")
	ErrExpired         = errors.New("approval record expired")
	ErrInvalidDecision = errors.New("invalid approval decision")
)

// NewID returns an opaque, unguessable approval identifier.
func NewID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", errors.Wrap(err, "failed to generate approval id")
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ApplyDefaults fills the record's ID, state, timestamps and expiry. ttl <= 0
// uses DefaultTTL.
func ApplyDefaults(rec *Record, now time.Time, ttl time.Duration) error {
	if rec == nil {
		return errors.New("approval record is nil")
	}
	if rec.ID == "" {
		id, err := NewID()
		if err != nil {
			return err
		}
		rec.ID = id
	}
	if rec.State == "" {
		rec.State = StatePending
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	if rec.ExpiresAt.IsZero() {
		if ttl <= 0 {
			ttl = DefaultTTL
		}
		rec.ExpiresAt = rec.CreatedAt.Add(ttl)
	}
	return nil
}

// ExpireIfNeeded transitions a pending record past its expiry to StateExpired.
// It reports whether the record changed.
func ExpireIfNeeded(rec *Record, now time.Time) bool {
	if rec == nil || rec.State != StatePending || rec.ExpiresAt.IsZero() || now.Before(rec.ExpiresAt) {
		return false
	}
	rec.State = StateExpired
	return true
}

// Clone returns a deep copy of the record.
func Clone(rec *Record) *Record {
	if rec == nil {
		return nil
	}
	out := *rec
	out.Args = append(json.RawMessage(nil), rec.Args...)
	out.Requester.Groups = append([]string(nil), rec.Requester.Groups...)
	if rec.DecidedAt != nil {
		t := *rec.DecidedAt
		out.DecidedAt = &t
	}
	return &out
}

// PollDecision implements WaitDecision by polling Get at a fixed interval. It
// is the shared fallback for backends without a native wake-up mechanism.
func PollDecision(ctx context.Context, s ApprovalStore, id string, wait, interval time.Duration) (*Record, error) {
	if wait <= 0 {
		return s.Get(ctx, id)
	}
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	deadline := time.Now().Add(wait)
	for {
		rec, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if rec.State != StatePending {
			return rec, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return rec, nil
		}
		sleep := interval
		if sleep > remaining {
			sleep = remaining
		}
		timer := time.NewTimer(sleep)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
