package feed

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/alexherrero/sherwood/internal/store"
)

// Upload is one stored upload.
type Upload struct {
	ID int64
	// At is when the NAS took the upload in.
	At time.Time
	// Accepted and Dropped count the items stored and dropped.
	Accepted int
	Dropped  int
}

// Stored is one stored item, with the upload it came in.
type Stored struct {
	ID         int64
	UploadID   int64
	UploadedAt time.Time
	Item
}

// Querier is what the readers read through: a *sql.DB (such as
// store.Reader()) or a *sql.Tx.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Save stores a checked feed as one upload, taken in at at, with its kept
// items, in one transaction on w (store.Writer()). It only ever adds rows.
func Save(ctx context.Context, w *sql.DB, at time.Time, f Feed) (Upload, error) {
	tx, err := w.BeginTx(ctx, nil)
	if err != nil {
		return Upload{}, fmt.Errorf("feed: saving an upload: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	up := Upload{At: at, Accepted: len(f.Items), Dropped: len(f.Dropped)}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO feed_uploads (at, accepted, dropped) VALUES (?, ?, ?)`,
		store.FormatTime(at), up.Accepted, up.Dropped)
	if err != nil {
		return Upload{}, fmt.Errorf("feed: saving an upload: %w", err)
	}
	if up.ID, err = res.LastInsertId(); err != nil {
		return Upload{}, fmt.Errorf("feed: saving an upload: %w", err)
	}
	for _, it := range f.Items {
		sources, err := json.Marshal(it.Sources)
		if err != nil {
			return Upload{}, fmt.Errorf("feed: saving an item: %w", err)
		}
		var key any
		if it.Kind == SignalExplanation {
			key = it.SignalKey
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO feed_items (upload_id, kind, symbol, signal_key, text, sources)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			up.ID, string(it.Kind), it.Symbol, key, it.Text, string(sources)); err != nil {
			return Upload{}, fmt.Errorf("feed: saving a %s for %s: %w", it.Kind, it.Symbol, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Upload{}, fmt.Errorf("feed: saving an upload: %w", err)
	}
	return up, nil
}

// LatestUpload reads the newest upload, or nil when there has been none.
func LatestUpload(ctx context.Context, q Querier) (*Upload, error) {
	var up Upload
	var at string
	err := q.QueryRowContext(ctx,
		`SELECT id, at, accepted, dropped FROM feed_uploads ORDER BY id DESC LIMIT 1`).
		Scan(&up.ID, &at, &up.Accepted, &up.Dropped)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("feed: reading the latest upload: %w", err)
	}
	if up.At, err = store.ParseTime(at); err != nil {
		return nil, fmt.Errorf("feed: reading upload %d: %w", up.ID, err)
	}
	return &up, nil
}

// itemColumns are the columns readItems scans, from feed_items i joined to
// feed_uploads u.
const itemColumns = `i.id, i.upload_id, u.at, i.kind, i.symbol, coalesce(i.signal_key, ''), i.text, i.sources`

// LatestCandidates reads the candidates of the newest upload, in the file's
// order. It is empty when there has been no upload, or the newest had none.
func LatestCandidates(ctx context.Context, q Querier) ([]Stored, error) {
	return readItems(ctx, q,
		`SELECT `+itemColumns+` FROM feed_items i JOIN feed_uploads u ON u.id = i.upload_id
		 WHERE i.upload_id = (SELECT max(id) FROM feed_uploads) AND i.kind = 'candidate'
		 ORDER BY i.id`)
}

// LatestNotes reads the newest holding note on each of held, from any
// upload, keyed by symbol. A held stock no upload has a note on is absent.
func LatestNotes(ctx context.Context, q Querier, held []string) (map[string]Stored, error) {
	out := map[string]Stored{}
	if len(held) == 0 {
		return out, nil
	}
	want := map[string]bool{}
	for _, s := range held {
		want[s] = true
	}
	notes, err := readItems(ctx, q,
		`SELECT `+itemColumns+` FROM feed_items i JOIN feed_uploads u ON u.id = i.upload_id
		 WHERE i.id IN (SELECT max(id) FROM feed_items WHERE kind = 'holding_note' GROUP BY symbol)
		 ORDER BY i.symbol`)
	if err != nil {
		return nil, err
	}
	for _, n := range notes {
		if want[n.Symbol] {
			out[n.Symbol] = n
		}
	}
	return out, nil
}

// Explanation reads the newest explanation of the signal with key, or nil
// when there is none.
func Explanation(ctx context.Context, q Querier, key string) (*Stored, error) {
	items, err := readItems(ctx, q,
		`SELECT `+itemColumns+` FROM feed_items i JOIN feed_uploads u ON u.id = i.upload_id
		 WHERE i.kind = 'signal_explanation' AND i.signal_key = ?
		 ORDER BY i.id DESC LIMIT 1`, key)
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return &items[0], nil
}

func readItems(ctx context.Context, q Querier, query string, args ...any) ([]Stored, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("feed: reading items: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Stored{}
	for rows.Next() {
		var s Stored
		var at, kind, sources string
		if err := rows.Scan(&s.ID, &s.UploadID, &at, &kind, &s.Symbol, &s.SignalKey, &s.Text, &sources); err != nil {
			return nil, fmt.Errorf("feed: reading items: %w", err)
		}
		s.Kind = Kind(kind)
		if s.UploadedAt, err = store.ParseTime(at); err != nil {
			return nil, fmt.Errorf("feed: reading item %d: %w", s.ID, err)
		}
		if err := json.Unmarshal([]byte(sources), &s.Sources); err != nil {
			return nil, fmt.Errorf("feed: reading item %d's sources: %w", s.ID, err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("feed: reading items: %w", err)
	}
	return out, nil
}
