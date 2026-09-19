package api

import (
	"net/http"

	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/keel/wire"
	"github.com/nrynss/reprise/internal/memory"
)

// threadMinEpisodes floors the episode span of a name thread. A name in
// fewer episodes recurs, but it threads no gallery row yet.
const threadMinEpisodes = 2

// Threads serves the cross episode thread index. Create it with
// NewThreads, because the zero value holds no database. Mount wires it
// under the threads table pattern behind the spend gate and the guest
// middleware.
type Threads struct {
	db *sqlite.DB
}

// NewThreads returns the threads route on one handler. A nil database
// answers 500, so wiring faults surface instead of hiding.
func NewThreads(db *sqlite.DB) http.Handler {
	h := &Threads{db: db}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/threads", h.index)
	return mux
}

// threadHitJSON is one appearance of a thread in one episode. The quote
// is the stored wording, and the offset points at its rendered words.
type threadHitJSON struct {
	// EpisodeID is the episode holding the appearance.
	EpisodeID string `json:"episode_id"`
	// Number orders the episode within its owner.
	Number int `json:"number"`
	// Quote is the stored wording as heard.
	Quote string `json:"quote"`
	// Offset is the rendered word index the mention points at.
	Offset int `json:"offset"`
}

// nameThreadJSON is one recurring name with every episode behind it.
// Every count comes from stored rows, so a spoken count repeats these
// numbers.
type nameThreadJSON struct {
	// Key is the normalised name every appearance folds into.
	Key string `json:"key"`
	// Display is the most recent stored wording.
	Display string `json:"display"`
	// Kind is the provider entity kind behind the thread.
	Kind string `json:"kind"`
	// Episodes holds every appearance, oldest episode first.
	Episodes []threadHitJSON `json:"episodes"`
	// MentionCount counts the stored mentions behind the thread.
	MentionCount int `json:"mention_count"`
	// EpisodeCount counts the distinct episodes behind the thread.
	EpisodeCount int `json:"episode_count"`
}

// circledTopicJSON is one key phrase the speaker keeps circling with no
// close on record.
type circledTopicJSON struct {
	// Key is the normalised phrase every appearance folds into.
	Key string `json:"key"`
	// Display is the most recent stored wording.
	Display string `json:"display"`
	// Episodes holds every appearance, oldest episode first.
	Episodes []threadHitJSON `json:"episodes"`
	// MentionCount counts the stored mentions behind the topic.
	MentionCount int `json:"mention_count"`
	// EpisodeCount counts the distinct episodes behind the topic.
	EpisodeCount int `json:"episode_count"`
}

// threadsJSON is the thread index across the owner episodes.
type threadsJSON struct {
	// NameThreads holds the recurring names, most returned to first.
	NameThreads []nameThreadJSON `json:"name_threads"`
	// CircledTopics holds the phrases with no close on record.
	CircledTopics []circledTopicJSON `json:"circled_topics"`
}

// index answers GET /api/threads with the owner thread index. Every
// number comes from stored rows, so the gallery repeats a query result.
func (h *Threads) index(w http.ResponseWriter, r *http.Request) {
	owner, ok := ownerOf(w, r)
	if !ok {
		return
	}
	if h.db == nil {
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the thread store is not wired", nil)
		return
	}
	names, err := memory.RecurringNames(r.Context(), h.db.Reader(), owner, threadMinEpisodes)
	if err != nil {
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the threads could not be read", nil)
		return
	}
	circled, err := memory.CircledTopics(r.Context(), h.db.Reader(), owner)
	if err != nil {
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the threads could not be read", nil)
		return
	}
	threads := make([]nameThreadJSON, 0, len(names))
	for _, thread := range names {
		hits := make([]threadHitJSON, 0, len(thread.Episodes))
		for _, hit := range thread.Episodes {
			hits = append(hits, threadHitJSON{
				EpisodeID: hit.EpisodeID,
				Number:    hit.Number,
				Quote:     hit.Quote,
				Offset:    hit.Offset,
			})
		}
		threads = append(threads, nameThreadJSON{
			Key:          thread.Key,
			Display:      thread.Display,
			Kind:         thread.Kind,
			Episodes:     hits,
			MentionCount: thread.MentionCount,
			EpisodeCount: thread.EpisodeCount,
		})
	}
	topics := make([]circledTopicJSON, 0, len(circled))
	for _, topic := range circled {
		hits := make([]threadHitJSON, 0, len(topic.Episodes))
		for _, hit := range topic.Episodes {
			hits = append(hits, threadHitJSON{
				EpisodeID: hit.EpisodeID,
				Number:    hit.Number,
				Quote:     hit.Quote,
				Offset:    hit.Offset,
			})
		}
		topics = append(topics, circledTopicJSON{
			Key:          topic.Key,
			Display:      topic.Display,
			Episodes:     hits,
			MentionCount: topic.MentionCount,
			EpisodeCount: topic.EpisodeCount,
		})
	}
	writeJSON(w, http.StatusOK, threadsJSON{NameThreads: threads, CircledTopics: topics})
}
