// Diary access for the session broker lives here.
package broker

import (
	"context"
	"errors"
	"fmt"

	"github.com/nrynss/keel/id"
	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/episode"
)

// ErrDiary reports a diary argument the broker cannot honour, such as a nil
// database.
var ErrDiary = errors.New("broker: invalid diary")

// Diary persists the rows one started session needs. The SQLite diary
// implements it.
type Diary interface {
	// CountSessions reports how many sessions the owner started before.
	CountSessions(ctx context.Context, ownerID string) (int, error)
	// CreateEpisodeAndSession inserts one recording episode and its session
	// row, and returns both ids. The provider session id stays empty until
	// the voice pass learns it.
	CreateEpisodeAndSession(ctx context.Context, ownerID string, capSeconds int) (episodeID string, sessionID string, err error)
}

// SQLiteDiary is a Diary over the shared diary database. Create it with
// NewSQLiteDiary, because the zero value has no database. A SQLiteDiary is
// safe for concurrent use.
type SQLiteDiary struct {
	db *sqlite.DB
}

// NewSQLiteDiary returns a Diary over db. The diary schema must already be
// open on db.
func NewSQLiteDiary(db *sqlite.DB) (*SQLiteDiary, error) {
	if db == nil {
		return nil, fmt.Errorf("broker: new diary: %w: database must not be nil", ErrDiary)
	}
	return &SQLiteDiary{db: db}, nil
}

// CountSessions reports how many sessions the owner started before.
func (d *SQLiteDiary) CountSessions(ctx context.Context, ownerID string) (int, error) {
	var held int
	if err := d.db.Reader().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sessions WHERE owner_id = ?", ownerID).Scan(&held); err != nil {
		return 0, fmt.Errorf("broker: count sessions: %w", err)
	}
	return held, nil
}

// CreateEpisodeAndSession inserts one recording episode and its session row,
// and returns both ids. The episode takes the next number for its owner with
// a plain title, because the session start is the only entry to the episode
// lifecycle. A session insert failure removes the episode it just made, so
// the refusal leaves no orphan behind.
func (d *SQLiteDiary) CreateEpisodeAndSession(ctx context.Context, ownerID string, capSeconds int) (string, string, error) {
	var number int64
	if err := d.db.Reader().QueryRowContext(ctx,
		"SELECT COALESCE(MAX(number), 0) FROM episodes WHERE owner_id = ?", ownerID).Scan(&number); err != nil {
		return "", "", fmt.Errorf("broker: next episode number: %w", err)
	}
	number++
	episodeID, err := id.New()
	if err != nil {
		return "", "", fmt.Errorf("broker: mint episode id: %w", err)
	}
	sessionID, err := id.New()
	if err != nil {
		return "", "", fmt.Errorf("broker: mint session id: %w", err)
	}
	if err := episode.Begin(ctx, d.db, episode.NewEpisode{
		ID:      episodeID,
		OwnerID: ownerID,
		Number:  number,
		Title:   fmt.Sprintf("Episode %d", number),
	}); err != nil {
		return "", "", fmt.Errorf("broker: begin episode: %w", err)
	}
	if _, err := d.db.Writer().ExecContext(ctx,
		`INSERT INTO sessions (id, owner_id, episode_id, provider_session_id, token_cap, connected_seconds)
		VALUES (?, ?, ?, '', ?, 0)`, sessionID, ownerID, episodeID, capSeconds); err != nil {
		_, _ = d.db.Writer().ExecContext(ctx, "DELETE FROM episodes WHERE id = ?", episodeID)
		return "", "", fmt.Errorf("broker: insert session row: %w", err)
	}
	return episodeID, sessionID, nil
}
