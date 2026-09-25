// Owner scoped bundle reads and the HTTP route that serves them.
//
// Every query scopes on the owner and the episode together. Seeded
// copies share catalog blob bytes but never rows, so the bundle names
// only the requesting owner's episode, words, chapters, and cover. A
// stranger reads the same 404 as a missing episode.

package export

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/nrynss/keel/ffmpeg"
	"github.com/nrynss/keel/job"
	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/keel/wire"
	"github.com/nrynss/reprise/internal/analysis"
)

// KindName is the job kind the export bundle registers under. The kind
// runs inline in the request, so it declares no resume work. A restart
// never reruns it, because no export job outlives its request.
const KindName = "export"

// PatternExport serves one export bundle behind the owner routes.
// Unknown and foreign ids both answer 404.
const PatternExport = "/api/episodes/{id}/export"

// Envelope codes the screen branches on. Codes stay stable, wording
// stays free.
const (
	// CodeNotFound answers a missing or foreign episode with one shape.
	CodeNotFound = "not_found"
	// CodeNotReady answers an episode that never finished.
	CodeNotReady = "export_not_ready"
	// CodeInternal answers a fault the caller cannot fix.
	CodeInternal = "internal"
	// CodeMethodNotAllowed answers a wrong method on the live route.
	CodeMethodNotAllowed = "method_not_allowed"
)

// OwnerCheck reports whether the request owns ownerID. The wiring
// passes the guest session check, so a request with no session owns
// nothing.
type OwnerCheck interface {
	// Owns reports whether the request user owns ownerID.
	Owns(ctx context.Context, ownerID string) bool
}

// Config configures a Service. DB is the shared diary handle. MediaDir
// holds one blob file per persisted id, CoverDir holds one cover file
// per episode. Tools names the media binaries. Owns checks episode
// ownership per request.
type Config struct {
	// DB is the shared diary handle.
	DB *sqlite.DB
	// MediaDir holds the blob files the renders reference.
	MediaDir string
	// CoverDir holds one cover file per episode named for its episode.
	CoverDir string
	// Tools names the ffmpeg and ffprobe executables.
	Tools Tools
	// Owns checks episode ownership per request.
	Owns OwnerCheck
}

// Service builds and serves export bundles. Create it with New,
// because the zero value holds no store. A Service is safe for
// concurrent use.
type Service struct {
	db       *sqlite.DB
	mediaDir string
	coverDir string
	tools    Tools
	owns     OwnerCheck
}

// New validates cfg and returns the Service.
func New(cfg Config) (*Service, error) {
	if cfg.DB == nil {
		return nil, fmt.Errorf("%w: database must not be nil", ErrInvalid)
	}
	if cfg.MediaDir == "" {
		return nil, fmt.Errorf("%w: media directory must not be empty", ErrInvalid)
	}
	if cfg.CoverDir == "" {
		return nil, fmt.Errorf("%w: cover directory must not be empty", ErrInvalid)
	}
	if cfg.Owns == nil {
		return nil, fmt.Errorf("%w: ownership check must not be nil", ErrInvalid)
	}
	return &Service{db: cfg.DB, mediaDir: cfg.MediaDir, coverDir: cfg.CoverDir, tools: cfg.Tools, owns: cfg.Owns}, nil
}

// Kind returns the job kind that reserves the export name. The bundle
// builds inline in the request, so the kind carries a limit and no
// resume path. Register it under KindName before the runner opens, so
// a second registration refuses instead of replacing it.
func (s *Service) Kind() job.Kind {
	return job.Kind{Limit: 2}
}

// Handler returns the export route. Mount it behind the guest session
// middleware and the spend gate, so only the owner reaches it.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(PatternExport, s.serveExport)
	return mux
}

// serveExport answers GET with one zip bundle. Unknown and foreign ids
// share the 404, an unfinished episode reads as conflict, and anything
// else reads as internal.
func (s *Service) serveExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeRefusal(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "exports read with GET")
		return
	}
	episodeID := r.PathValue("id")
	ownerID, err := s.lookupOwner(r.Context(), episodeID)
	if err != nil {
		writeExportError(w, err)
		return
	}
	if !s.owns.Owns(r.Context(), ownerID) {
		writeRefusal(w, http.StatusNotFound, CodeNotFound, "that episode opens nothing")
		return
	}
	if err := s.ServeBundle(w, r, ownerID, episodeID); err != nil {
		writeExportError(w, err)
		return
	}
}

// ServeBundle builds the bundle for one owner held episode and serves
// it as a download. It refuses a foreign or missing episode and an
// episode that never finished. The zip streams from a temp file that is
// removed before return, so failures never leave bytes behind.
func (s *Service) ServeBundle(w http.ResponseWriter, r *http.Request, ownerID, episodeID string) error {
	in, err := s.loadInputs(r.Context(), ownerID, episodeID)
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "export-serve-*")
	if err != nil {
		return fmt.Errorf("export: serve bundle: %w", err)
	}
	defer os.RemoveAll(work)
	zipPath := filepath.Join(work, "bundle.zip")
	out, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("export: serve bundle: %w", err)
	}
	buildErr := WriteBundle(r.Context(), s.tools, in, out)
	closeErr := out.Close()
	if buildErr != nil {
		return buildErr
	}
	if closeErr != nil {
		return fmt.Errorf("export: serve bundle: %w", closeErr)
	}
	zipFile, err := os.Open(zipPath)
	if err != nil {
		return fmt.Errorf("export: serve bundle: %w", err)
	}
	defer zipFile.Close()
	info, err := zipFile.Stat()
	if err != nil {
		return fmt.Errorf("export: serve bundle: %w", err)
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", bundleName(in.Number)))
	http.ServeContent(w, r, bundleName(in.Number), info.ModTime(), zipFile)
	return nil
}

// bundleName names the download for one episode number. The name
// carries the number alone, because titles carry punctuation the
// header would have to escape.
func bundleName(number int64) string {
	return fmt.Sprintf("episode-%d-bundle.zip", number)
}

// episodeRow is the episode state one bundle describes.
type episodeRow struct {
	// Number orders the episode within its season.
	Number int64
	// Title names the episode.
	Title string
	// State is the lifecycle state.
	State string
}

// chapterLine decodes one stored chapter. It mirrors the stored shape,
// so the bundle reads what the analysis pass wrote.
type chapterLine struct {
	// Title is the chapter title.
	Title string `json:"title"`
	// StartMs is the chapter start in milliseconds from the render start.
	StartMs int64 `json:"start_ms"`
}

// summaryDoc decodes the stored summary. Only the running text feeds
// the description, because the headline and bullets already shaped the
// proposals.
type summaryDoc struct {
	// Block is the running text summary.
	Block string `json:"block_summary"`
}

// lookupOwner returns the owner behind one episode id, or ErrNotFound
// when no episode carries it. It names no owner to the caller.
func (s *Service) lookupOwner(ctx context.Context, episodeID string) (string, error) {
	if episodeID == "" {
		return "", fmt.Errorf("export: owner of %q: %w", episodeID, ErrNotFound)
	}
	var ownerID string
	err := s.db.Reader().QueryRowContext(ctx,
		`SELECT owner_id FROM episodes WHERE id = ?`, episodeID).Scan(&ownerID)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("export: owner of %q: %w", episodeID, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("export: owner of %q: %w", episodeID, err)
	}
	return ownerID, nil
}

// loadInputs gathers one bundle from owner scoped rows. Every query
// names the owner beside the episode, so a seeded copy reads its own
// rows even where it shares blob bytes with the catalog. A ready state
// gates the build, because only a finished episode carries the render,
// words, chapters, and cover together.
func (s *Service) loadInputs(ctx context.Context, ownerID, episodeID string) (Inputs, error) {
	var row episodeRow
	err := s.db.Reader().QueryRowContext(ctx,
		`SELECT number, title, state FROM episodes WHERE id = ? AND owner_id = ?`,
		episodeID, ownerID).Scan(&row.Number, &row.Title, &row.State)
	if err == sql.ErrNoRows {
		return Inputs{}, fmt.Errorf("export: bundle for %q: %w", episodeID, ErrNotOwner)
	}
	if err != nil {
		return Inputs{}, fmt.Errorf("export: bundle for %q: %w", episodeID, err)
	}
	if row.State != "ready" {
		return Inputs{}, fmt.Errorf("export: bundle for %q in %s: %w", episodeID, row.State, ErrNotReady)
	}
	audio, blob, err := s.loadAudio(ctx, ownerID, episodeID)
	if err != nil {
		return Inputs{}, err
	}
	words, err := s.loadWords(ctx, ownerID, episodeID)
	if err != nil {
		return Inputs{}, err
	}
	chapters, notes, err := s.loadChapters(ctx, ownerID, episodeID)
	if err != nil {
		return Inputs{}, err
	}
	cover, err := s.loadCover(ctx, ownerID, episodeID)
	if err != nil {
		return Inputs{}, err
	}
	duration, err := ffmpeg.Duration(ctx, ffmpeg.Tools(s.tools), filepath.Join(s.mediaDir, blob))
	if err != nil {
		return Inputs{}, fmt.Errorf("export: bundle for %q: %w", episodeID, err)
	}
	return Inputs{
		Title:      row.Title,
		Number:     row.Number,
		Audio:      audio,
		Cover:      cover,
		Words:      words,
		Chapters:   chapters,
		ShowNotes:  notes,
		DurationMs: duration.Milliseconds(),
	}, nil
}

// newestAudioBlob names the AAC blob the newest render row carries for
// one owner held episode, or empty when no render exists.
func (s *Service) newestAudioBlob(ctx context.Context, ownerID, episodeID string) (string, error) {
	var blob string
	err := s.db.Reader().QueryRowContext(ctx,
		`SELECT aac_media_id FROM renders WHERE episode_id = ? AND owner_id = ? AND aac_media_id != ''
		 ORDER BY rowid DESC LIMIT 1`, episodeID, ownerID).Scan(&blob)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("export: audio for %q: %w: no render", episodeID, ErrNotReady)
	}
	if err != nil {
		return "", fmt.Errorf("export: audio for %q: %w", episodeID, err)
	}
	return blob, nil
}

// loadAudio reads the AAC render bytes for one owner held episode from
// the media directory the blob store writes. It also names the blob the
// duration probe measures, so the probe reads the file the bundle
// carries. The bytes travel into the bundle unchanged, so ffprobe reads
// the same file the render wrote.
func (s *Service) loadAudio(ctx context.Context, ownerID, episodeID string) ([]byte, string, error) {
	blob, err := s.newestAudioBlob(ctx, ownerID, episodeID)
	if err != nil {
		return nil, "", err
	}
	raw, err := os.ReadFile(filepath.Join(s.mediaDir, blob))
	if err != nil {
		return nil, "", fmt.Errorf("export: audio for %q: %w", episodeID, err)
	}
	if len(raw) == 0 {
		return nil, "", fmt.Errorf("export: audio for %q: %w: empty render", episodeID, ErrInvalid)
	}
	return raw, blob, nil
}

// loadWords reads the rendered words for one owner held episode in
// timeline order. The captions group them, so order decides every cue.
func (s *Service) loadWords(ctx context.Context, ownerID, episodeID string) ([]Word, error) {
	rows, err := s.db.Reader().QueryContext(ctx,
		`SELECT text, start_ms, end_ms FROM words
		 WHERE episode_id = ? AND owner_id = ? AND source = ?
		 ORDER BY start_ms ASC, rowid ASC`, episodeID, ownerID, analysis.SourceRendered)
	if err != nil {
		return nil, fmt.Errorf("export: words for %q: %w", episodeID, err)
	}
	defer rows.Close()
	var out []Word
	for rows.Next() {
		var word Word
		if err := rows.Scan(&word.Text, &word.StartMs, &word.EndMs); err != nil {
			return nil, fmt.Errorf("export: words for %q: %w", episodeID, err)
		}
		out = append(out, word)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("export: words for %q: %w", episodeID, err)
	}
	return out, nil
}

// loadChapters reads the chapters and show notes the analysis pass
// stored for one owner held episode. The newest analyses row wins, so
// a repeated pass never serves stale markers.
func (s *Service) loadChapters(ctx context.Context, ownerID, episodeID string) ([]Chapter, string, error) {
	var chaptersRaw, summaryRaw string
	err := s.db.Reader().QueryRowContext(ctx,
		`SELECT chapters, summary FROM analyses WHERE episode_id = ? AND owner_id = ?
		 ORDER BY rowid DESC LIMIT 1`, episodeID, ownerID).Scan(&chaptersRaw, &summaryRaw)
	if err == sql.ErrNoRows {
		return nil, "", fmt.Errorf("export: chapters for %q: %w: no analysis", episodeID, ErrNotReady)
	}
	if err != nil {
		return nil, "", fmt.Errorf("export: chapters for %q: %w", episodeID, err)
	}
	var lines []chapterLine
	if err := json.Unmarshal([]byte(chaptersRaw), &lines); err != nil {
		return nil, "", fmt.Errorf("export: chapters for %q: %w", episodeID, err)
	}
	chapters := make([]Chapter, 0, len(lines))
	for _, line := range lines {
		chapters = append(chapters, Chapter(line))
	}
	var summary summaryDoc
	if err := json.Unmarshal([]byte(summaryRaw), &summary); err != nil {
		return nil, "", fmt.Errorf("export: notes for %q: %w", episodeID, err)
	}
	return chapters, summary.Block, nil
}

// loadCover reads the cover file the cover pass stored for one owner
// held episode. The row names the file, and the bytes travel into the
// bundle unchanged.
func (s *Service) loadCover(ctx context.Context, ownerID, episodeID string) ([]byte, error) {
	var file string
	err := s.db.Reader().QueryRowContext(ctx,
		`SELECT file FROM covers WHERE episode_id = ? AND owner_id = ?`, episodeID, ownerID).Scan(&file)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("export: cover for %q: %w: no cover", episodeID, ErrNotReady)
	}
	if err != nil {
		return nil, fmt.Errorf("export: cover for %q: %w", episodeID, err)
	}
	raw, err := os.ReadFile(filepath.Join(s.coverDir, file))
	if err != nil {
		return nil, fmt.Errorf("export: cover for %q: %w", episodeID, err)
	}
	return raw, nil
}

// writeExportError maps package sentinels to refusal codes. Missing
// and foreign episodes share the 404, an unfinished episode reads as
// conflict, and anything else reads as internal.
func writeExportError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound) || errors.Is(err, ErrNotOwner):
		writeRefusal(w, http.StatusNotFound, CodeNotFound, "that episode opens nothing")
	case errors.Is(err, ErrNotReady):
		writeRefusal(w, http.StatusConflict, CodeNotReady, "that episode never finished, so nothing exports yet")
	default:
		writeRefusal(w, http.StatusInternalServerError, CodeInternal, "that request could not finish")
	}
}

// writeRefusal answers through the shared error envelope. Screens
// branch on the code and never on the message.
func writeRefusal(w http.ResponseWriter, status int, code, message string) {
	_ = wire.WriteError(w, status, code, message, nil)
}
