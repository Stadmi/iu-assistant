// Package store persists the active knowledge base, vectors and bounded chat history.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"iu-assistant/internal/knowledge"
	_ "modernc.org/sqlite"
)

type Store struct {
	db   *sql.DB
	Path string
}
type Indexed struct {
	Record      knowledge.Record `json:"record"`
	Vector      []float64        `json:"vector"`
	Model       string           `json:"model"`
	Fingerprint string           `json:"fingerprint"`
}
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL;
	CREATE TABLE IF NOT EXISTS records(id TEXT PRIMARY KEY, payload TEXT NOT NULL);
	CREATE TABLE IF NOT EXISTS history(id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER NOT NULL, role TEXT NOT NULL, content TEXT NOT NULL);
	CREATE INDEX IF NOT EXISTS history_user ON history(user_id,id);
	CREATE TABLE IF NOT EXISTS meta(key TEXT PRIMARY KEY, value TEXT NOT NULL);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, Path: path}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Records(ctx context.Context) ([]Indexed, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT payload FROM records ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Indexed{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var r Indexed
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Replace stages everything before atomically replacing the working records.
// The JSON backup contains a complete, restorable previous index.
func (s *Store) Replace(ctx context.Context, records []Indexed) error {
	old, err := s.Records(ctx)
	if err != nil {
		return err
	}
	if len(old) > 0 {
		dir := filepath.Join(filepath.Dir(s.Path), "backups")
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		raw, err := json.MarshalIndent(old, "", "  ")
		if err != nil {
			return err
		}
		path := filepath.Join(dir, "index-"+time.Now().UTC().Format("20060102T150405.000000000")+".json")
		if err := os.WriteFile(path, raw, 0600); err != nil {
			return fmt.Errorf("резервная копия: %w", err)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM records"); err != nil {
		return err
	}
	for _, r := range records {
		raw, err := json.Marshal(r)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO records(id,payload) VALUES(?,?)", r.Record.ID, string(raw)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) History(ctx context.Context, uid int64, pairs int) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT role,content FROM (SELECT id,role,content FROM history WHERE user_id=? ORDER BY id DESC LIMIT ?) ORDER BY id`, uid, pairs*2)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.Role, &m.Content); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SaveTurnWithRecords commits the conversation and its verified topic together.
// Record IDs are context, not facts, and are scoped to one private user.
func (s *Store) SaveTurnWithRecords(ctx context.Context, uid int64, question, answer string, pairs int, recordIDs []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, m := range []Message{{"user", question}, {"assistant", answer}} {
		if _, err := tx.ExecContext(ctx, "INSERT INTO history(user_id,role,content) VALUES(?,?,?)", uid, m.Role, m.Content); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM history WHERE user_id=? AND id NOT IN (SELECT id FROM history WHERE user_id=? ORDER BY id DESC LIMIT ?)`, uid, uid, pairs*2)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("dialogue_records:%d", uid)
	if len(recordIDs) == 0 {
		_, err = tx.ExecContext(ctx, "DELETE FROM meta WHERE key=?", key)
	} else {
		raw, e := json.Marshal(recordIDs)
		if e != nil {
			return e
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, string(raw))
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) LastRecordIDs(ctx context.Context, uid int64) ([]string, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key=?", fmt.Sprintf("dialogue_records:%d", uid)).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, fmt.Errorf("некорректное состояние диалога: выполните /reset")
	}
	return ids, nil
}

func (s *Store) Reset(ctx context.Context, uid int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM history WHERE user_id=?", uid); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM meta WHERE key=?", fmt.Sprintf("dialogue_records:%d", uid)); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Offset(ctx context.Context) (int64, error) {
	var v int64
	err := s.db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key='telegram_offset'").Scan(&v)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return v, err
}
func (s *Store) SetOffset(ctx context.Context, v int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES('telegram_offset',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, v)
	return err
}
