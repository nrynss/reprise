package episode

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/nrynss/keel/sqlite"
)

// Outcome is the latest transcript class job outcome for one episode. The
// completion answer and the episode detail both carry it, so the caller
// sees what the last pass did instead of silence. Found is false when no
// pass ever started for the episode.
type Outcome struct {
	// Found reports whether any pass started for the episode.
	Found bool
	// JobID identifies the latest pass.
	JobID string
	// Status is the latest pass state, such as running or error.
	Status string
	// Error carries the terminal failure text, or empty when the pass
	// did not fail.
	Error string
}

// progressSnap decodes the snapshot the schedule stamps on its job. The
// episode linkage rides inside detail, which only the application reads.
type progressSnap struct {
	// Detail carries the raw episode linkage the schedule stamped.
	Detail json.RawMessage `json:"detail"`
}

// linkage describes the episode stamp every schedule writes on its job
// before returning, so a later schedule maps the job back to its
// episode. It matches the shape the binary stamps, read here only.
type linkage struct {
	// OwnerID scopes the episode the job worked.
	OwnerID string `json:"owner_id"`
	// EpisodeID scopes the episode the job worked.
	EpisodeID string `json:"episode_id"`
	// RenderID names the render a pass after the render worked on. The
	// passes before the render leave it empty.
	RenderID string `json:"render_id,omitempty"`
}

// likeEscape escapes the LIKE wildcards in one id, so the outcome query
// matches the literal id only.
func likeEscape(id string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(id)
}

// LastKindJob returns the latest job of one kind whose stamped linkage
// names the episode. It reads the durable job rows through the shared
// file, because the schedule stamps every start synchronously and the
// outcome must survive a restart. A job without linkage names no episode
// and never matches. It reports Found false when no pass ever started.
func LastKindJob(ctx context.Context, db *sqlite.DB, episodeID, kind string) (Outcome, error) {
	return lastJob(ctx, db, episodeID, kind, func(linkage) bool { return true })
}

// LastRenderKindJob returns the latest job of one kind whose stamped
// linkage names both the episode and one render. The passes after a
// render stamp the render they work on, so a pass that ran on an older
// render never counts for a newer one. It reports Found false when no
// pass ever started on that render.
func LastRenderKindJob(ctx context.Context, db *sqlite.DB, episodeID, renderID, kind string) (Outcome, error) {
	if renderID == "" {
		return Outcome{}, fmt.Errorf("episode: last job %q: %w: empty render", episodeID, ErrInvalid)
	}
	return lastJob(ctx, db, episodeID, kind, func(link linkage) bool { return link.RenderID == renderID })
}

// lastJob returns the latest job of one kind whose linkage names the
// episode and passes match.
func lastJob(ctx context.Context, db *sqlite.DB, episodeID, kind string, match func(linkage) bool) (Outcome, error) {
	if db == nil || episodeID == "" || kind == "" {
		return Outcome{}, fmt.Errorf("episode: last job %q: %w", episodeID, ErrInvalid)
	}
	pattern := `%"episode_id":"` + likeEscape(episodeID) + `"%`
	rows, err := db.Reader().QueryContext(ctx,
		`SELECT id, status, progress, error FROM jobs
		 WHERE kind = ? AND progress LIKE ? ESCAPE '\'
		 ORDER BY updated_at DESC, id DESC`, kind, pattern)
	if err != nil {
		return Outcome{}, fmt.Errorf("episode: last job %q: %w", episodeID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, status, progress, errText string
		if err := rows.Scan(&id, &status, &progress, &errText); err != nil {
			return Outcome{}, fmt.Errorf("episode: last job %q: %w", episodeID, err)
		}
		var snap progressSnap
		if err := json.Unmarshal([]byte(progress), &snap); err != nil || len(snap.Detail) == 0 {
			continue
		}
		var link linkage
		if err := json.Unmarshal(snap.Detail, &link); err != nil || link.EpisodeID == "" {
			continue
		}
		if link.EpisodeID != episodeID || !match(link) {
			continue
		}
		return Outcome{Found: true, JobID: id, Status: status, Error: errText}, nil
	}
	if err := rows.Err(); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Outcome{}, nil
		}
		return Outcome{}, fmt.Errorf("episode: last job %q: %w", episodeID, err)
	}
	return Outcome{}, nil
}

// TranscriptOutcome returns the latest transcript pass outcome for an
// episode the owner holds. Unknown and foreign ids both report
// ErrNotFound, so callers answer 404 either way. It reports Found false
// when the service names no transcript kind or no pass ever started, so
// processes without a runner still serve the detail.
func (s *Service) TranscriptOutcome(ctx context.Context, ownerID, episodeID string) (Outcome, error) {
	if s == nil || s.db == nil || ownerID == "" || episodeID == "" {
		return Outcome{}, fmt.Errorf("episode: transcript outcome %q: %w", episodeID, ErrInvalid)
	}
	if _, err := s.Get(ctx, ownerID, episodeID); err != nil {
		return Outcome{}, err
	}
	if s.transcriptKind == "" {
		return Outcome{}, nil
	}
	out, err := LastKindJob(ctx, s.db, episodeID, s.transcriptKind)
	if err != nil {
		return Outcome{}, err
	}
	return out, nil
}
