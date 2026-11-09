// Package postgresql implements an ApprovalStore backed by PostgreSQL.
// Decisions are delivered to long-pollers through LISTEN/NOTIFY; GC is a
// separate job (see GC).
package postgresql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"emperror.dev/errors"
	"github.com/lib/pq"
	_ "github.com/lib/pq"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
	"github.com/webcenter-fr/eino-ext/server/approval/store"
)

var tablePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Config configures the PostgreSQL store.
type Config struct {
	// DSN is the PostgreSQL connection string.
	DSN string `validate:"required" json:"-" jsonschema:"(required) PostgreSQL connection string"`
	// Table is the records table. Default "approval_records".
	Table string `validate:"omitempty" jsonschema:"description=Records table (default 'approval_records')"`
	// TTL is the default record TTL. Default 15m.
	TTL time.Duration `validate:"omitempty" jsonschema:"description=Default record TTL (default 15m)"`
	// Retention is how long decided/expired records are kept. Default 1h.
	Retention time.Duration `validate:"omitempty" jsonschema:"description=Retention after expiry (default 1h)"`
}

// Store is a PostgreSQL-backed ApprovalStore.
type Store struct {
	db        *sql.DB
	dsn       string
	table     string
	ttl       time.Duration
	retention time.Duration
}

// New builds a PostgreSQL store, verifies connectivity and creates the table.
func New(ctx context.Context, cfg *Config) (*Store, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.Table == "" {
		cfg.Table = "approval_records"
	}
	if cfg.TTL <= 0 {
		cfg.TTL = store.DefaultTTL
	}
	if cfg.Retention <= 0 {
		cfg.Retention = store.DefaultRetention
	}
	if err := validate.Struct(cfg); err != nil {
		return nil, err
	}
	if !tablePattern.MatchString(cfg.Table) {
		return nil, errors.Errorf("invalid PostgreSQL table name %q", cfg.Table)
	}
	db, err := sql.Open("postgres", cfg.DSN)
	if err != nil {
		return nil, errors.Wrap(err, "failed to open PostgreSQL connection")
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, errors.Wrap(err, "failed to connect to PostgreSQL")
	}
	s := &Store{db: db, dsn: cfg.DSN, table: cfg.Table, ttl: cfg.TTL, retention: cfg.Retention}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	statements := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id TEXT PRIMARY KEY,
			server_name TEXT NOT NULL,
			tool_name TEXT NOT NULL,
			instance TEXT NOT NULL DEFAULT '',
			operation TEXT NOT NULL DEFAULT '',
			args JSONB NOT NULL,
			args_hash TEXT NOT NULL,
			preview TEXT NOT NULL DEFAULT '',
			preview_warning TEXT NOT NULL DEFAULT '',
			requester JSONB NOT NULL,
			state TEXT NOT NULL,
			policy TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL,
			expires_at TIMESTAMPTZ NOT NULL,
			decided_at TIMESTAMPTZ,
			approver_sub TEXT NOT NULL DEFAULT '',
			approver_user TEXT NOT NULL DEFAULT '',
			decision TEXT NOT NULL DEFAULT '',
			receipt_jti TEXT NOT NULL DEFAULT ''
		)`, s.table),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_pending_idx ON %s (state, created_at)`, s.table, s.table),
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return errors.Wrap(err, "failed to migrate approval records table")
		}
	}
	return nil
}

// Create implements store.ApprovalStore.
func (s *Store) Create(ctx context.Context, rec *store.Record) error {
	if rec == nil {
		return errors.New("approval record is nil")
	}
	if err := store.ApplyDefaults(rec, time.Now(), s.ttl); err != nil {
		return err
	}
	args, err := json.Marshal(rec.Args)
	if err != nil {
		return errors.Wrap(err, "failed to encode approval args")
	}
	requester, err := json.Marshal(rec.Requester)
	if err != nil {
		return errors.Wrap(err, "failed to encode approval requester")
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s
		(id, server_name, tool_name, instance, operation, args, args_hash, preview, preview_warning,
		 requester, state, policy, created_at, expires_at, decided_at, approver_sub, approver_user, decision, receipt_jti)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`, s.table),
		rec.ID, rec.ServerName, rec.ToolName, rec.Instance, rec.Operation, args, rec.ArgsHash,
		rec.Preview, rec.PreviewWarning, requester, rec.State, rec.Policy, rec.CreatedAt, rec.ExpiresAt,
		rec.DecidedAt, rec.ApproverSub, rec.ApproverUser, rec.Decision, rec.ReceiptJTI)
	if err != nil {
		return errors.Wrap(err, "failed to insert approval record")
	}
	return nil
}

// Get implements store.ApprovalStore.
func (s *Store) Get(ctx context.Context, id string) (*store.Record, error) {
	rec, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	if store.ExpireIfNeeded(rec, time.Now()) {
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET state=$1 WHERE id=$2`, s.table), rec.State, rec.ID); err != nil {
			return nil, errors.Wrap(err, "failed to expire approval record")
		}
	}
	return rec, nil
}

// ListPending implements store.ApprovalStore.
func (s *Store) ListPending(ctx context.Context, filter store.ListFilter) ([]*store.Record, error) {
	now := time.Now()
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET state=$1 WHERE state=$2 AND expires_at <= $3`, s.table),
		store.StateExpired, store.StatePending, now); err != nil {
		return nil, errors.Wrap(err, "failed to expire overdue approvals")
	}
	query := fmt.Sprintf(`SELECT id, server_name, tool_name, instance, operation, args, args_hash, preview,
		preview_warning, requester, state, policy, created_at, expires_at, decided_at, approver_sub,
		approver_user, decision, receipt_jti FROM %s WHERE state=$1`, s.table)
	args := []any{store.StatePending}
	if filter.ServerName != "" {
		args = append(args, filter.ServerName)
		query += fmt.Sprintf(" AND server_name=$%d", len(args))
	}
	if filter.Instance != "" {
		args = append(args, filter.Instance)
		query += fmt.Sprintf(" AND instance=$%d", len(args))
	}
	if filter.ToolName != "" {
		args = append(args, filter.ToolName)
		query += fmt.Sprintf(" AND tool_name=$%d", len(args))
	}
	query += " ORDER BY created_at ASC"
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list pending approvals")
	}
	defer func() { _ = rows.Close() }()
	out := make([]*store.Record, 0)
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "failed to iterate pending approvals")
	}
	return out, nil
}

// Decide implements store.ApprovalStore.
func (s *Store) Decide(ctx context.Context, id string, d store.Decision) (*store.Record, error) {
	if d.Decision != store.DecisionApprove && d.Decision != store.DecisionReject {
		return nil, store.ErrInvalidDecision
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to begin approval decision transaction")
	}
	defer func() { _ = tx.Rollback() }()

	var state string
	var expiresAt time.Time
	err = tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT state, expires_at FROM %s WHERE id=$1 FOR UPDATE`, s.table), id).Scan(&state, &expiresAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, errors.Wrap(err, "failed to lock approval record")
	}
	now := time.Now()
	if state == string(store.StateExpired) || !now.Before(expiresAt) {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET state=$1 WHERE id=$2`, s.table), store.StateExpired, id); err != nil {
			return nil, errors.Wrap(err, "failed to expire approval record")
		}
		if err := tx.Commit(); err != nil {
			return nil, errors.Wrap(err, "failed to commit approval expiry")
		}
		return nil, store.ErrExpired
	}
	if state != string(store.StatePending) {
		return nil, store.ErrAlreadyDecided
	}
	newState := store.StateApproved
	if d.Decision == store.DecisionReject {
		newState = store.StateRejected
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET state=$1, decision=$2, approver_sub=$3,
		approver_user=$4, policy=$5, decided_at=$6 WHERE id=$7`, s.table),
		newState, d.Decision, d.ApproverSub, d.ApproverUser, d.Policy, now, id); err != nil {
		return nil, errors.Wrap(err, "failed to update approval decision")
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_notify('approval_decided', $1)`, id); err != nil {
		return nil, errors.Wrap(err, "failed to notify approval decision")
	}
	if err := tx.Commit(); err != nil {
		return nil, errors.Wrap(err, "failed to commit approval decision")
	}
	return s.Get(ctx, id)
}

// WaitDecision implements store.ApprovalStore using LISTEN/NOTIFY.
func (s *Store) WaitDecision(ctx context.Context, id string, wait time.Duration) (*store.Record, error) {
	if wait <= 0 {
		return s.Get(ctx, id)
	}
	listener := pq.NewListener(s.dsn, 10*time.Second, time.Minute, nil)
	defer func() { _ = listener.Close() }()
	if err := listener.Listen("approval_decided"); err != nil {
		return nil, errors.Wrap(err, "failed to listen for approval decisions")
	}
	rec, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if rec.State != store.StatePending {
		return rec, nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return s.Get(ctx, id)
		case notification := <-listener.Notify:
			if notification != nil && notification.Extra == id {
				return s.Get(ctx, id)
			}
		}
	}
}

// GC implements store.ApprovalStore.
func (s *Store) GC(ctx context.Context, now time.Time) (int, error) {
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET state=$1 WHERE state=$2 AND expires_at <= $3`, s.table),
		store.StateExpired, store.StatePending, now); err != nil {
		return 0, errors.Wrap(err, "failed to expire overdue approvals")
	}
	result, err := s.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE expires_at < $1`, s.table), now.Add(-s.retention))
	if err != nil {
		return 0, errors.Wrap(err, "failed to delete expired approvals")
	}
	deleted, _ := result.RowsAffected()
	return int(deleted), nil
}

func (s *Store) get(ctx context.Context, id string) (*store.Record, error) {
	row := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT id, server_name, tool_name, instance, operation, args,
		args_hash, preview, preview_warning, requester, state, policy, created_at, expires_at, decided_at,
		approver_sub, approver_user, decision, receipt_jti FROM %s WHERE id=$1`, s.table), id)
	rec, err := scanRecord(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	return rec, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanRecord(row scanner) (*store.Record, error) {
	var rec store.Record
	var args, requester []byte
	var decidedAt sql.NullTime
	if err := row.Scan(&rec.ID, &rec.ServerName, &rec.ToolName, &rec.Instance, &rec.Operation, &args,
		&rec.ArgsHash, &rec.Preview, &rec.PreviewWarning, &requester, &rec.State, &rec.Policy,
		&rec.CreatedAt, &rec.ExpiresAt, &decidedAt, &rec.ApproverSub, &rec.ApproverUser,
		&rec.Decision, &rec.ReceiptJTI); err != nil {
		return nil, err
	}
	rec.Args = json.RawMessage(args)
	if err := json.Unmarshal(requester, &rec.Requester); err != nil {
		return nil, errors.Wrap(err, "failed to decode approval requester")
	}
	if decidedAt.Valid {
		t := decidedAt.Time
		rec.DecidedAt = &t
	}
	return &rec, nil
}

var _ store.ApprovalStore = (*Store)(nil)
