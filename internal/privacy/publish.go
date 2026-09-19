// Publish and unpublish for diary episodes.
//
// Publishing mints a fresh unguessable token and marks the finished
// render public, so the share link behind the token plays audio to a
// signed-out visitor. Only the streaming render goes public. The export
// copy, the stems, the transcripts and the threads stay private, because
// no share route reaches them. Unpublishing rotates the token and
// returns every render blob to private, so the old link dies at once.

package privacy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/nrynss/keel/id"
)

// episode holds the columns publish and unpublish read and write.
type episode struct {
	id         string
	owner      string
	visibility string
	token      string
}

// loadRow reads one episode row with no ownership check. Callers
// check ownership through the Owns seam next, so a forged owner id
// never passes here.
func (s *Service) loadRow(ctx context.Context, episodeID string) (episode, error) {
	var out episode
	err := s.db.Reader().QueryRowContext(ctx,
		"SELECT id, owner_id, visibility, share_token FROM episodes WHERE id = ?", episodeID).Scan(
		&out.id, &out.owner, &out.visibility, &out.token)
	if errors.Is(err, sql.ErrNoRows) {
		return episode{}, fmt.Errorf("privacy: episode %s: %w", episodeID, ErrNotFound)
	}
	if err != nil {
		return episode{}, fmt.Errorf("privacy: load episode %s: %w", episodeID, err)
	}
	return out, nil
}

// owned loads one episode row and checks the request owns it. A missing
// row reports ErrNotFound, and a foreign row reports ErrNotOwner. Both
// map to 404 at the handler, so neither answer confirms a private
// episode.
func (s *Service) owned(ctx context.Context, episodeID string) (episode, error) {
	out, err := s.loadRow(ctx, episodeID)
	if err != nil {
		return episode{}, err
	}
	if !s.owns.Owns(ctx, out.owner) {
		return episode{}, fmt.Errorf("privacy: episode %s: %w", episodeID, ErrNotOwner)
	}
	return out, nil
}

// renderBlobs returns the streaming and export blob ids of the latest
// render row. A missing render row reports ErrNoRender, because a share
// link with no audio promises what it cannot play.
func (s *Service) renderBlobs(ctx context.Context, episodeID string) (opus, aac string, err error) {
	err = s.db.Reader().QueryRowContext(ctx,
		`SELECT opus_media_id, aac_media_id FROM renders
		 WHERE episode_id = ? ORDER BY rowid DESC LIMIT 1`, episodeID).Scan(&opus, &aac)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", fmt.Errorf("privacy: episode %s: %w", episodeID, ErrNoRender)
	}
	if err != nil {
		return "", "", fmt.Errorf("privacy: load render %s: %w", episodeID, err)
	}
	return opus, aac, nil
}

// setVisibility flips one media row between public and private. The
// media store exposes no visibility flip, so this update names the
// value the store reads on its next lookup. Bytes and ids stay stable,
// which keeps existing URLs valid across the flip.
func (s *Service) setVisibility(ctx context.Context, blobID, visibility string) error {
	res, err := s.db.Writer().ExecContext(ctx,
		"UPDATE media SET visibility = ? WHERE id = ?", visibility, blobID)
	if err != nil {
		return fmt.Errorf("privacy: mark blob %s: %w", blobID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("privacy: mark blob %s: %w", blobID, err)
	}
	if affected == 0 {
		return fmt.Errorf("privacy: blob %s: %w", blobID, ErrMediaMissing)
	}
	return nil
}

// Publish opens episodeID behind a fresh share token. It mints the
// token, marks the episode public, and marks the streaming render
// public. The export copy stays private, because the share page plays
// the stream and never offers the export. Publishing an already public
// episode rotates its token again, so every publish hands out exactly
// one live link.
func (s *Service) Publish(ctx context.Context, episodeID string) (string, error) {
	if episodeID == "" {
		return "", fmt.Errorf("privacy: publish: %w: empty episode", ErrInvalid)
	}
	ep, err := s.owned(ctx, episodeID)
	if err != nil {
		return "", err
	}
	opus, _, err := s.renderBlobs(ctx, ep.id)
	if err != nil {
		return "", err
	}
	token, err := id.New()
	if err != nil {
		return "", fmt.Errorf("privacy: publish %s: %w", episodeID, err)
	}
	if _, err := s.db.Writer().ExecContext(ctx,
		"UPDATE episodes SET visibility = ?, share_token = ? WHERE id = ? AND owner_id = ?",
		VisibilityPublic, token, ep.id, ep.owner); err != nil {
		return "", fmt.Errorf("privacy: publish %s: %w", episodeID, err)
	}
	if err := s.setVisibility(ctx, opus, "public"); err != nil {
		return "", err
	}
	return token, nil
}

// Unpublish closes episodeID. It marks the episode private, rotates
// the token so the old link dies, and returns the opus and export
// blobs of every render row to private. A superseded render keeps its
// blob id, so only a sweep over all rows revokes the whole history. A
// missing blob row is tolerated here, because the rotated token
// already killed the link and revoking must never fail open.
func (s *Service) Unpublish(ctx context.Context, episodeID string) (string, error) {
	if episodeID == "" {
		return "", fmt.Errorf("privacy: unpublish: %w: empty episode", ErrInvalid)
	}
	ep, err := s.owned(ctx, episodeID)
	if err != nil {
		return "", err
	}
	token, err := id.New()
	if err != nil {
		return "", fmt.Errorf("privacy: unpublish %s: %w", episodeID, err)
	}
	if _, err := s.db.Writer().ExecContext(ctx,
		"UPDATE episodes SET visibility = ?, share_token = ? WHERE id = ? AND owner_id = ?",
		VisibilityPrivate, token, ep.id, ep.owner); err != nil {
		return "", fmt.Errorf("privacy: unpublish %s: %w", episodeID, err)
	}
	rows, err := s.db.Reader().QueryContext(ctx,
		`SELECT opus_media_id, aac_media_id FROM renders WHERE episode_id = ?`, ep.id)
	if err != nil {
		return "", fmt.Errorf("privacy: unpublish %s: %w", episodeID, err)
	}
	var blobs []string
	for rows.Next() {
		var opus, aac string
		if err := rows.Scan(&opus, &aac); err != nil {
			_ = rows.Close()
			return "", fmt.Errorf("privacy: unpublish %s: %w", episodeID, err)
		}
		blobs = append(blobs, opus, aac)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("privacy: unpublish %s: %w", episodeID, err)
	}
	for _, blobID := range blobs {
		if blobID == "" {
			continue
		}
		if _, err := s.db.Writer().ExecContext(ctx,
			"UPDATE media SET visibility = ? WHERE id = ?", "private", blobID); err != nil {
			return "", fmt.Errorf("privacy: unpublish %s: %w", episodeID, err)
		}
	}
	return token, nil
}
