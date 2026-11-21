package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"time"

	_ "modernc.org/sqlite"

	"github.com/zszz3/agentkeel/internal/event"
)

const schema = `
CREATE TABLE IF NOT EXISTS events (
	session_id TEXT    NOT NULL,
	seq        INTEGER NOT NULL,
	type       TEXT    NOT NULL,
	payload    TEXT    NOT NULL,
	created_at TEXT    NOT NULL,
	PRIMARY KEY (session_id, seq)
);`

type SQLite struct {
	db *sql.DB
}

// OpenSQLite 打开（或创建）一个 SQLite 文件。
//   - WAL：读不挡写，进程被 kill 后已提交的事务不会丢
//   - busy_timeout：两个进程同时写时，后到的等一会儿而不是立刻报错
//   - _txlock=immediate：事务一开始就拿写锁，Append 里“查最大 seq 再插入”这两步之间不会被别人插队
func OpenSQLite(path string) (*SQLite, error) {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Set("_txlock", "immediate")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: create schema: %w", err)
	}
	return &SQLite{db: db}, nil
}

func (s *SQLite) Append(ctx context.Context, e event.Event) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var last int64
	err = tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq), 0) FROM events WHERE session_id = ?`, e.SessionID,
	).Scan(&last)
	if err != nil {
		return err
	}
	if e.Seq != last+1 {
		return fmt.Errorf("%w: session %s wants seq %d, next is %d", ErrConflict, e.SessionID, e.Seq, last+1)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO events (session_id, seq, type, payload, created_at) VALUES (?, ?, ?, ?, ?)`,
		e.SessionID, e.Seq, string(e.Type), string(e.Payload), e.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLite) Load(ctx context.Context, sessionID string) ([]event.Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq, type, payload, created_at FROM events WHERE session_id = ? ORDER BY seq`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []event.Event{}
	for rows.Next() {
		var (
			e       event.Event
			typ     string
			payload string
			created string
		)
		if err := rows.Scan(&e.Seq, &typ, &payload, &created); err != nil {
			return nil, err
		}
		e.SessionID = sessionID
		e.Type = event.Type(typ)
		e.Payload = []byte(payload)
		if e.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, fmt.Errorf("store: seq %d: %w", e.Seq, err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *SQLite) Sessions(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT session_id FROM events WHERE seq = 1 ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *SQLite) Close() error { return s.db.Close() }

var _ Store = (*SQLite)(nil)

// IsConflict 判断 err 是不是 ErrConflict。
func IsConflict(err error) bool { return errors.Is(err, ErrConflict) }
