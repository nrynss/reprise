// The first visit read behind the welcome screen.
//
// Welcome reports the season one visitor sees before they record. Copies
// come first: a visitor who already holds seeded rows opens on their
// latest copy. Owners hold no copies, and a visitor who dropped every
// copy holds none either, so both fall back to the catalog. An empty
// catalog reports no teaser, and the screen offers only the record
// button.

package seed

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Welcome is the season behind the first visit read. Number is zero when
// the catalog holds no episode. Lines carry the first two mention quotes
// behind the teaser, and stay empty when it holds no mentions.
// AudioBlobID names the newest render blob, and stays empty when no
// render exists.
type Welcome struct {
	// Episodes counts the rows behind the teaser: the visitor copies
	// when present, else the catalog.
	Episodes int
	// Number orders the teaser in its season.
	Number int
	// Title names the teaser episode.
	Title string
	// FirstLine is the first mention quote behind the teaser.
	FirstLine string
	// SecondLine is the second mention quote, or empty.
	SecondLine string
	// AudioBlobID is the newest render opus blob, or empty.
	AudioBlobID string
}

// Welcome returns the season userID sees on the first visit. Copies win
// over the catalog, and the catalog wins over nothing. It reads only, so
// the caller copies first.
func (s *Service) Welcome(ctx context.Context, userID string) (Welcome, error) {
	if userID == "" {
		return Welcome{}, wrapInvalid("user id must not be empty")
	}
	teaser, err := s.latestSeeded(ctx, userID)
	if err != nil {
		return Welcome{}, err
	}
	if teaser.Number == 0 {
		return s.latestCatalog(ctx)
	}
	return teaser, nil
}

// teaserRow is one teaser episode with its owner, so the lines and the
// audio read under the same owner the episode belongs to.
type teaserRow struct {
	owner   string
	id      string
	welcome Welcome
}

// latestSeeded returns the newest seeded episode userID holds, or a zero
// teaser when they hold none.
func (s *Service) latestSeeded(ctx context.Context, userID string) (Welcome, error) {
	row, err := s.latestEpisode(ctx, userID, true)
	if err != nil {
		return Welcome{}, err
	}
	if row == nil {
		return Welcome{}, nil
	}
	if err := s.fillTeaser(ctx, row); err != nil {
		return Welcome{}, err
	}
	return row.welcome, nil
}

// latestCatalog returns the newest catalog episode, or a zero teaser
// when the catalog is empty.
func (s *Service) latestCatalog(ctx context.Context) (Welcome, error) {
	row, err := s.latestEpisode(ctx, SeedUserID, false)
	if err != nil {
		return Welcome{}, err
	}
	if row == nil {
		return Welcome{}, nil
	}
	if err := s.fillTeaser(ctx, row); err != nil {
		return Welcome{}, err
	}
	return row.welcome, nil
}

// latestEpisode reads the newest episode one owner holds with its season
// count. Seeded limits the read to seeded copies. A missing row is not
// an error.
func (s *Service) latestEpisode(ctx context.Context, owner string, seeded bool) (*teaserRow, error) {
	query := `SELECT id, number, title FROM episodes WHERE owner_id = ?`
	if seeded {
		query += ` AND seeded = 1`
	}
	query += ` ORDER BY number DESC LIMIT 1`
	row := &teaserRow{owner: owner}
	err := s.db.Reader().QueryRowContext(ctx, query, owner).Scan(&row.id, &row.welcome.Number, &row.welcome.Title)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("seed: read teaser episode: %w", err)
	}
	countQuery := `SELECT COUNT(*) FROM episodes WHERE owner_id = ?`
	if seeded {
		countQuery += ` AND seeded = 1`
	}
	if err := s.db.Reader().QueryRowContext(ctx, countQuery, owner).Scan(&row.welcome.Episodes); err != nil {
		return nil, fmt.Errorf("seed: count teaser season: %w", err)
	}
	return row, nil
}

// fillTeaser reads the mention lines and the render audio behind one
// teaser episode.
func (s *Service) fillTeaser(ctx context.Context, row *teaserRow) error {
	lines, err := s.teaserLines(ctx, row)
	if err != nil {
		return err
	}
	if len(lines) > 0 {
		row.welcome.FirstLine = lines[0]
	}
	if len(lines) > 1 {
		row.welcome.SecondLine = lines[1]
	}
	audio, err := s.teaserAudio(ctx, row)
	if err != nil {
		return err
	}
	row.welcome.AudioBlobID = audio
	return nil
}

// teaserLines reads the first two mention quotes behind one teaser
// episode, oldest first.
func (s *Service) teaserLines(ctx context.Context, row *teaserRow) ([]string, error) {
	rows, err := s.db.Reader().QueryContext(ctx,
		`SELECT quote FROM mentions WHERE episode_id = ? AND owner_id = ? ORDER BY rowid ASC LIMIT 2`,
		row.id, row.owner)
	if err != nil {
		return nil, fmt.Errorf("seed: read teaser lines: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var quote string
		if err := rows.Scan(&quote); err != nil {
			return nil, fmt.Errorf("seed: read teaser lines: %w", err)
		}
		out = append(out, quote)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("seed: read teaser lines: %w", err)
	}
	return out, nil
}

// teaserAudio reads the newest render opus blob behind one teaser
// episode, or empty when no render exists.
func (s *Service) teaserAudio(ctx context.Context, row *teaserRow) (string, error) {
	var blob string
	err := s.db.Reader().QueryRowContext(ctx,
		`SELECT opus_media_id FROM renders
		 WHERE episode_id = ? AND owner_id = ? AND opus_media_id != ''
		 ORDER BY rowid DESC LIMIT 1`, row.id, row.owner).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("seed: read teaser audio: %w", err)
	}
	return blob, nil
}
