package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"tcpadapter/internal/queue"
)

const pgInitSchema = `
CREATE TABLE IF NOT EXISTS adapter_sessions (
	controller_id TEXT PRIMARY KEY,
	last_seen TIMESTAMPTZ NOT NULL,
	buffer_free INTEGER NOT NULL,
	fw_mode BOOLEAN NOT NULL,
	sync_mode BOOLEAN NOT NULL,
	pending JSONB NOT NULL,
	inflight JSONB NOT NULL,
	next_seq SMALLINT NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(dsn string) (*PostgresStore, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(30 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	if _, err := db.ExecContext(ctx, pgInitSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init postgres schema: %w", err)
	}
	return &PostgresStore{db: db}, nil
}

func (s *PostgresStore) SaveSession(snapshot SessionSnapshot) error {
	pending, err := json.Marshal(snapshot.Pending)
	if err != nil {
		return fmt.Errorf("marshal pending: %w", err)
	}
	inFlight, err := json.Marshal(snapshot.InFlight)
	if err != nil {
		return fmt.Errorf("marshal inflight: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO adapter_sessions (
	controller_id, last_seen, buffer_free, fw_mode, sync_mode, pending, inflight, next_seq, updated_at
) VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7::jsonb,$8,NOW())
ON CONFLICT (controller_id) DO UPDATE SET
	last_seen = EXCLUDED.last_seen,
	buffer_free = EXCLUDED.buffer_free,
	fw_mode = EXCLUDED.fw_mode,
	sync_mode = EXCLUDED.sync_mode,
	pending = EXCLUDED.pending,
	inflight = EXCLUDED.inflight,
	next_seq = EXCLUDED.next_seq,
	updated_at = NOW()
`,
		snapshot.ControllerID,
		snapshot.LastSeen,
		snapshot.BufferFree,
		snapshot.FWMode,
		snapshot.SyncMode,
		string(pending),
		string(inFlight),
		int(snapshot.NextSeq),
	)
	if err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	return nil
}

func (s *PostgresStore) DeleteSession(controllerID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM adapter_sessions WHERE controller_id = $1`, controllerID); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (s *PostgresStore) ListSessions() ([]SessionSnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, `
SELECT controller_id, last_seen, buffer_free, fw_mode, sync_mode, pending, inflight, next_seq
FROM adapter_sessions
`)
	if err != nil {
		return nil, fmt.Errorf("list sessions query: %w", err)
	}
	defer rows.Close()

	out := make([]SessionSnapshot, 0)
	for rows.Next() {
		var (
			snap        SessionSnapshot
			pendingRaw  []byte
			inFlightRaw []byte
			nextSeqInt  int
		)
		if err := rows.Scan(
			&snap.ControllerID,
			&snap.LastSeen,
			&snap.BufferFree,
			&snap.FWMode,
			&snap.SyncMode,
			&pendingRaw,
			&inFlightRaw,
			&nextSeqInt,
		); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		if len(pendingRaw) == 0 {
			snap.Pending = make([]queue.Command, 0)
		} else if err := json.Unmarshal(pendingRaw, &snap.Pending); err != nil {
			return nil, fmt.Errorf("unmarshal pending: %w", err)
		}
		if len(inFlightRaw) == 0 {
			snap.InFlight = make([]InFlightSnapshot, 0)
		} else if err := json.Unmarshal(inFlightRaw, &snap.InFlight); err != nil {
			return nil, fmt.Errorf("unmarshal inflight: %w", err)
		}
		if nextSeqInt < 0 || nextSeqInt > 255 {
			return nil, fmt.Errorf("invalid next_seq for %s: %d", snap.ControllerID, nextSeqInt)
		}
		snap.NextSeq = uint8(nextSeqInt)
		out = append(out, snap)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list sessions rows: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) Close() error {
	return s.db.Close()
}
