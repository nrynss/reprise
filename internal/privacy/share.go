// The signed-out share view.
//
// A share token opens exactly one published episode, and only its
// finished render and its cover. LookupShare resolves the token to the
// metadata the share page renders. The audio itself streams from the
// media store, which serves the public render blob to a visitor with no
// session. Cover serves the cover file the same way. No function here
// names a stem, a transcript, a thread, or another episode, so there is
// no route from a token to anything behind the episode.

package privacy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/nrynss/keel/id"
)

// Share is the metadata the share page renders for one token. AudioID
// is the public render blob the page streams from the media store.
// CoverPath is the cover endpoint behind the same token. Author is the
// owner display name or empty. It never carries an address.
type Share struct {
	// EpisodeID is the shared episode.
	EpisodeID string `json:"episode_id"`
	// Title is the episode title.
	Title string `json:"title"`
	// Number orders the episode within its owner.
	Number int64 `json:"number"`
	// Author is the owner display name, empty while unset.
	Author string `json:"author"`
	// AudioID is the public streaming render blob.
	AudioID string `json:"audio_media_id"`
	// CoverPath is the cover endpoint behind the same token.
	CoverPath string `json:"cover_path"`
}

// LookupShare resolves token to its published episode. An unknown, a
// revoked, or an unpublished token reports ErrNotFound, and all three
// share one answer, so no probe tells a visitor whether an episode
// exists. A published episode with no finished render reports
// ErrNotFound too, because publish refuses that state and the share
// page would have nothing to play.
func (s *Service) LookupShare(ctx context.Context, token string) (Share, error) {
	if !id.Valid(token) {
		return Share{}, fmt.Errorf("privacy: share: %w", ErrNotFound)
	}
	var out Share
	var number int64
	var title string
	var episodeID string
	var ownerID string
	err := s.db.Reader().QueryRowContext(ctx,
		`SELECT id, owner_id, title, number FROM episodes
		 WHERE share_token = ? AND visibility = ?`, token, VisibilityPublic).Scan(
		&episodeID, &ownerID, &title, &number)
	if errors.Is(err, sql.ErrNoRows) {
		return Share{}, fmt.Errorf("privacy: share: %w", ErrNotFound)
	}
	if err != nil {
		return Share{}, fmt.Errorf("privacy: lookup share: %w", err)
	}
	var audio string
	err = s.db.Reader().QueryRowContext(ctx,
		`SELECT opus_media_id FROM renders
		 WHERE episode_id = ? ORDER BY rowid DESC LIMIT 1`, episodeID).Scan(&audio)
	if errors.Is(err, sql.ErrNoRows) || audio == "" {
		return Share{}, fmt.Errorf("privacy: share %s: %w", episodeID, ErrNotFound)
	}
	if err != nil {
		return Share{}, fmt.Errorf("privacy: lookup share %s: %w", episodeID, err)
	}
	out = Share{
		EpisodeID: episodeID,
		Title:     title,
		Number:    number,
		Author:    s.authorOf(ctx, ownerID),
		AudioID:   audio,
		CoverPath: "/api/share/" + token + "/cover",
	}
	return out, nil
}

// authorOf returns the owner display name or empty. It never returns an
// address. A missing name column or a missing user reads empty, so older
// copies keep serving the share page while the migration catches up.
func (s *Service) authorOf(ctx context.Context, ownerID string) string {
	if ownerID == "" {
		return ""
	}
	var name sql.NullString
	if err := s.db.Reader().QueryRowContext(ctx,
		"SELECT display_name FROM users WHERE id = ?", ownerID).Scan(&name); err != nil {
		return ""
	}
	if !name.Valid {
		return ""
	}
	return name.String
}

// coverPath returns the cover file for an episode. The cover writer
// names each file for its episode, so a lookup needs no row.
func (s *Service) coverPath(episodeID string) string {
	return filepath.Join(s.coverDir, episodeID+".png")
}

// ServeCover writes the cover behind token. It resolves the token
// through LookupShare first, so an unpublished or revoked token ends
// here with 404 before any file is named.
func (s *Service) ServeCover(w http.ResponseWriter, r *http.Request, token string) {
	share, err := s.LookupShare(r.Context(), token)
	if err != nil {
		writeRefusal(w, http.StatusNotFound, CodeNotFound, "that episode opens nothing")
		return
	}
	f, err := os.Open(s.coverPath(share.EpisodeID))
	if errors.Is(err, os.ErrNotExist) {
		writeRefusal(w, http.StatusNotFound, CodeNotFound, "that episode opens nothing")
		return
	}
	if err != nil {
		writeRefusal(w, http.StatusInternalServerError, CodeInternal, "the cover could not be read")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeContent(w, r, "", zeroTime(), f)
}
