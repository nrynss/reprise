package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/nrynss/keel/job"
	"github.com/nrynss/keel/wire"
	"github.com/nrynss/reprise/internal/episode"
)

// episodeStore is the episode domain the episode routes read and write.
// The episode service implements it. Handlers declare the seam, so tests
// bind a fake without opening a database.
type episodeStore interface {
	// List returns every episode the owner holds.
	List(ctx context.Context, ownerID string) ([]episode.Episode, error)
	// Get returns one episode the owner holds.
	Get(ctx context.Context, ownerID, episodeID string) (episode.Episode, error)
	// Proposals returns every proposal on an episode the owner holds.
	Proposals(ctx context.Context, ownerID, episodeID string) ([]episode.Proposal, error)
	// Decide appends one decision row on a proposal the owner holds.
	Decide(ctx context.Context, ownerID, episodeID, proposalID, decision string) error
	// RequestRender moves a draft episode to rendering and starts its
	// render job, returning the job id.
	RequestRender(ctx context.Context, ownerID, episodeID string) (string, error)
	// TranscriptOutcome returns the latest transcript pass outcome for
	// an episode the owner holds.
	TranscriptOutcome(ctx context.Context, ownerID, episodeID string) (episode.Outcome, error)
	// EditorialOutcome returns the latest editorial pass outcome for an
	// episode the owner holds.
	EditorialOutcome(ctx context.Context, ownerID, episodeID string) (episode.Outcome, error)
	// EditWords returns edit-source words for an episode the owner holds,
	// oldest first, with times in seconds.
	EditWords(ctx context.Context, ownerID, episodeID string) ([]episode.EditWord, error)
	// RenderedWords returns words transcribed from the rendered file for
	// an episode the owner holds, oldest first, with times in seconds.
	RenderedWords(ctx context.Context, ownerID, episodeID string) ([]episode.EditWord, error)
	// StemMediaID returns the user stem media id, or the host stem when
	// no user stem is stored, or empty when neither is stored.
	StemMediaID(ctx context.Context, ownerID, episodeID string) (string, error)
	// RenderMediaID returns the newest render opus media id, or empty
	// when no render exists.
	RenderMediaID(ctx context.Context, ownerID, episodeID string) (string, error)
}

var _ episodeStore = (*episode.Service)(nil)

// Episodes serves the episode list, detail, decisions, and mark done
// routes. Create it with NewEpisodes, because the zero value holds no
// store. Mount wires one value under all four table patterns behind the
// spend gate and the guest middleware.
type Episodes struct {
	store episodeStore
}

// NewEpisodes returns the episode routes on one handler. A nil store
// answers 500, so wiring faults surface instead of hiding.
func NewEpisodes(store episodeStore) http.Handler {
	h := &Episodes{store: store}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/episodes", h.list)
	mux.HandleFunc("GET /api/episodes/{id}", h.detail)
	mux.HandleFunc("POST /api/episodes/{id}/decisions", h.decide)
	mux.HandleFunc("POST /api/episodes/{id}/done", h.done)
	return mux
}

// episodeJSON is one episode on the wire. State and visibility travel as
// the stored strings, so the gallery draws what the lifecycle holds.
type episodeJSON struct {
	// ID identifies the episode.
	ID string `json:"id"`
	// Number orders the episode within its owner.
	Number int64 `json:"number"`
	// Title names the episode.
	Title string `json:"title"`
	// State is the lifecycle state.
	State string `json:"state"`
	// Visibility is private until an explicit publish.
	Visibility string `json:"visibility"`
}

// episodeListJSON lists the owner episodes, oldest number first.
type episodeListJSON struct {
	// Episodes holds every episode the owner holds.
	Episodes []episodeJSON `json:"episodes"`
}

// proposalJSON is one stored proposal with its latest decision. Decision
// stays empty while the user never touched the proposal.
type proposalJSON struct {
	// ID identifies the proposal.
	ID string `json:"id"`
	// Kind names the proposal.
	Kind string `json:"kind"`
	// StartWord is the first timeline word the proposal covers.
	StartWord int `json:"start_word"`
	// EndWord is the last timeline word the proposal covers.
	EndWord int `json:"end_word"`
	// Reason carries the one line reason behind the proposal.
	Reason string `json:"reason"`
	// Decision is the latest decision, or empty while untouched.
	Decision string `json:"decision"`
}

// wordJSON is one edit word on the wire. Start and End are seconds.
type wordJSON struct {
	// Text is the word as stored.
	Text string `json:"text"`
	// Start is the word start in seconds.
	Start float64 `json:"start"`
	// End is the word end in seconds.
	End float64 `json:"end"`
}

// episodeDetailJSON carries one episode with its proposals, edit words,
// and playable addresses. The editor reads the words and the stem
// address. The episode view plays the render address when one exists,
// and follows it with the rendered words.
// The transcript outcome rides beside them, so the detail agrees with
// the completion answer on what the last pass did.
type episodeDetailJSON struct {
	// Episode is the episode row.
	Episode episodeJSON `json:"episode"`
	// Proposals holds every proposal on the episode, oldest first.
	Proposals []proposalJSON `json:"proposals"`
	// TranscriptOutcome names the latest transcript pass and its state,
	// or nil when no pass ever started.
	TranscriptOutcome *transcriptOutcomeJSON `json:"transcript_outcome,omitempty"`
	// EditorialOutcome names the latest editorial pass and its state, or
	// nil when no editorial pass ever started. The transcript pass reads
	// done before the editorial pass stores its proposals, so a draft
	// waits on this pass too.
	EditorialOutcome *transcriptOutcomeJSON `json:"editorial_outcome,omitempty"`
	// Words holds edit-source words, oldest first.
	Words []wordJSON `json:"words"`
	// AudioURL is /media/{id} for the user stem, otherwise the host
	// stem, otherwise empty.
	AudioURL string `json:"audio_url"`
	// RenderAudioURL is /media/{id} for the newest render, or empty
	// when no render exists.
	RenderAudioURL string `json:"render_audio_url"`
	// RenderWords holds the words analysis transcribed from the render,
	// oldest first, on the render clock. It stays empty until analysis
	// stores them, and always while no render exists.
	RenderWords []wordJSON `json:"render_words"`
}

// transcriptOutcomeJSON carries one pass outcome on the wire. Error stays
// empty unless the pass failed, so screens branch on status first.
type transcriptOutcomeJSON struct {
	// JobID identifies the latest pass.
	JobID string `json:"job_id"`
	// Status is the latest pass state, such as running or error.
	Status string `json:"status"`
	// Error carries the terminal failure text, or empty otherwise.
	Error string `json:"error,omitempty"`
}

// decisionRequestJSON asks to accept or revert one proposal.
type decisionRequestJSON struct {
	// ProposalID identifies the proposal the decision names.
	ProposalID string `json:"proposal_id"`
	// Decision is accepted or reverted.
	Decision string `json:"decision"`
}

// decisionJSON echoes the stored decision.
type decisionJSON struct {
	// ProposalID identifies the decided proposal.
	ProposalID string `json:"proposal_id"`
	// Decision is the value just stored.
	Decision string `json:"decision"`
}

// doneJSON answers a mark done with the started render job.
type doneJSON struct {
	// EpisodeID identifies the rendering episode.
	EpisodeID string `json:"episode_id"`
	// JobID identifies the render job the gallery follows.
	JobID string `json:"job_id"`
	// State is the new lifecycle state.
	State string `json:"state"`
}

// episodeOf converts one stored episode to its wire shape.
func episodeOf(ep episode.Episode) episodeJSON {
	return episodeJSON{
		ID:         ep.ID,
		Number:     ep.Number,
		Title:      ep.Title,
		State:      string(ep.State),
		Visibility: ep.Visibility,
	}
}

// list answers GET /api/episodes with the owner episodes. A stranger
// lists nothing, because the store scopes on the request guest.
func (h *Episodes) list(w http.ResponseWriter, r *http.Request) {
	owner, ok := ownerOf(w, r)
	if !ok {
		return
	}
	if h.store == nil {
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the episode store is not wired", nil)
		return
	}
	eps, err := h.store.List(r.Context(), owner)
	if err != nil {
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the episodes could not be listed", nil)
		return
	}
	out := make([]episodeJSON, 0, len(eps))
	for _, ep := range eps {
		out = append(out, episodeOf(ep))
	}
	writeJSON(w, http.StatusOK, episodeListJSON{Episodes: out})
}

// detail answers GET /api/episodes/{id} with one episode and its
// proposals. Unknown and foreign ids both answer 404.
func (h *Episodes) detail(w http.ResponseWriter, r *http.Request) {
	owner, ok := ownerOf(w, r)
	if !ok {
		return
	}
	if h.store == nil {
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the episode store is not wired", nil)
		return
	}
	episodeID := r.PathValue("id")
	ep, err := h.store.Get(r.Context(), owner, episodeID)
	if errors.Is(err, episode.ErrNotFound) {
		_ = wire.WriteError(w, http.StatusNotFound, CodeEpisodeNotFound, "no episode lives at this id", nil)
		return
	}
	if err != nil {
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the episode could not be read", nil)
		return
	}
	props, err := h.store.Proposals(r.Context(), owner, episodeID)
	if err != nil {
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the proposals could not be read", nil)
		return
	}
	outcome, err := h.store.TranscriptOutcome(r.Context(), owner, episodeID)
	if err != nil {
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the pass outcome could not be read", nil)
		return
	}
	editorial, err := h.store.EditorialOutcome(r.Context(), owner, episodeID)
	if err != nil {
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the editorial outcome could not be read", nil)
		return
	}
	out := make([]proposalJSON, 0, len(props))
	for _, p := range props {
		out = append(out, proposalJSON{
			ID:        p.ID,
			Kind:      p.Kind,
			StartWord: p.StartWord,
			EndWord:   p.EndWord,
			Reason:    p.Reason,
			Decision:  p.Decision,
		})
	}
	stored, err := h.store.EditWords(r.Context(), owner, episodeID)
	if err != nil {
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the words could not be read", nil)
		return
	}
	stemID, err := h.store.StemMediaID(r.Context(), owner, episodeID)
	if err != nil {
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the stem could not be read", nil)
		return
	}
	renderID, err := h.store.RenderMediaID(r.Context(), owner, episodeID)
	if err != nil {
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the render could not be read", nil)
		return
	}
	var rendered []episode.EditWord
	if renderID != "" {
		rendered, err = h.store.RenderedWords(r.Context(), owner, episodeID)
		if err != nil {
			_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the rendered words could not be read", nil)
			return
		}
	}
	detail := episodeDetailJSON{
		Episode:        episodeOf(ep),
		Proposals:      out,
		Words:          wordsOf(stored),
		AudioURL:       mediaPath(stemID),
		RenderAudioURL: mediaPath(renderID),
		RenderWords:    wordsOf(rendered),
	}
	if outcome.Found {
		detail.TranscriptOutcome = &transcriptOutcomeJSON{
			JobID:  outcome.JobID,
			Status: outcome.Status,
			Error:  outcome.Error,
		}
	}
	if editorial.Found {
		detail.EditorialOutcome = &transcriptOutcomeJSON{
			JobID:  editorial.JobID,
			Status: editorial.Status,
			Error:  editorial.Error,
		}
	}
	writeJSON(w, http.StatusOK, detail)
}

// wordsOf converts stored words to their wire shape. It never returns
// nil, so the body always carries a list.
func wordsOf(stored []episode.EditWord) []wordJSON {
	out := make([]wordJSON, 0, len(stored))
	for _, word := range stored {
		out = append(out, wordJSON{Text: word.Text, Start: word.Start, End: word.End})
	}
	return out
}

// mediaPath is the owner media route for one blob, or empty when no
// blob is stored.
func mediaPath(id string) string {
	if id == "" {
		return ""
	}
	return "/media/" + id
}

// decide answers POST /api/episodes/{id}/decisions by appending one
// revertible decision row. Accept and revert are the only values.
func (h *Episodes) decide(w http.ResponseWriter, r *http.Request) {
	owner, ok := ownerOf(w, r)
	if !ok {
		return
	}
	if h.store == nil {
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the episode store is not wired", nil)
		return
	}
	var body decisionRequestJSON
	if !decodeBody(w, r, &body) {
		return
	}
	if body.ProposalID == "" || body.Decision == "" {
		_ = wire.WriteError(w, http.StatusBadRequest, CodeInvalidRequest, "a decision names a proposal and a value", nil)
		return
	}
	episodeID := r.PathValue("id")
	if _, err := h.store.Get(r.Context(), owner, episodeID); errors.Is(err, episode.ErrNotFound) {
		_ = wire.WriteError(w, http.StatusNotFound, CodeEpisodeNotFound, "no episode lives at this id", nil)
		return
	} else if err != nil {
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the episode could not be read", nil)
		return
	}
	if err := h.store.Decide(r.Context(), owner, episodeID, body.ProposalID, body.Decision); errors.Is(err, episode.ErrInvalid) {
		_ = wire.WriteError(w, http.StatusBadRequest, CodeInvalidRequest, "a decision is accepted or reverted", nil)
		return
	} else if errors.Is(err, episode.ErrNotFound) {
		_ = wire.WriteError(w, http.StatusNotFound, CodeProposalNotFound, "no proposal lives at this id", nil)
		return
	} else if err != nil {
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the decision could not be stored", nil)
		return
	}
	writeJSON(w, http.StatusOK, decisionJSON(body))
}

// done answers POST /api/episodes/{id}/done by moving a draft episode to
// rendering and starting its render job. Nothing else moves an episode,
// and a repeat tap reports the illegal move instead of starting again.
func (h *Episodes) done(w http.ResponseWriter, r *http.Request) {
	owner, ok := ownerOf(w, r)
	if !ok {
		return
	}
	if h.store == nil {
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the episode store is not wired", nil)
		return
	}
	episodeID := r.PathValue("id")
	if _, err := h.store.Get(r.Context(), owner, episodeID); errors.Is(err, episode.ErrNotFound) {
		_ = wire.WriteError(w, http.StatusNotFound, CodeEpisodeNotFound, "no episode lives at this id", nil)
		return
	} else if err != nil {
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the episode could not be read", nil)
		return
	}
	jobID, err := h.store.RequestRender(r.Context(), owner, episodeID)
	if errors.Is(err, episode.ErrIllegalTransition) {
		_ = wire.WriteError(w, http.StatusConflict, CodeIllegalTransition, "only a draft episode moves to rendering", nil)
		return
	}
	if errors.Is(err, job.ErrLimit) {
		_ = wire.WriteError(w, http.StatusTooManyRequests, CodeRenderBusy, "the render queue is full, retry this episode", nil)
		return
	}
	if errors.Is(err, episode.ErrStart) {
		_ = wire.WriteError(w, http.StatusServiceUnavailable, CodeRenderUnavailable, "the render job did not start, retry this episode", nil)
		return
	}
	if err != nil {
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the render job could not start", nil)
		return
	}
	writeJSON(w, http.StatusAccepted, doneJSON{EpisodeID: episodeID, JobID: jobID, State: string(episode.StateRendering)})
}
