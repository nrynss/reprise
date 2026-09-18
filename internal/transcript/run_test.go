package transcript_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nrynss/keel/cost"
	"github.com/nrynss/reprise/internal/assemblyai"
	"github.com/nrynss/reprise/internal/transcript"
)

// fakeBudget records every hold and frees nothing silently.
type fakeBudget struct {
	mu       sync.Mutex
	reserved []cost.Price
	settled  [][2]cost.Price
	released []cost.Price
	refuse   error
}

func (b *fakeBudget) Reserve(p cost.Price) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reserved = append(b.reserved, p)
	return b.refuse
}

func (b *fakeBudget) Settle(held, actual cost.Price) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.settled = append(b.settled, [2]cost.Price{held, actual})
	return nil
}

func (b *fakeBudget) Release(p cost.Price) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.released = append(b.released, p)
}

// providerFlow scripts one full provider exchange with a fixed word
// schedule on the uploaded stem.
type providerFlow struct {
	mu       sync.Mutex
	uploads  int
	creates  int
	keyterms []any
	deletes  int
	gets     int
	replies  []string
}

func (f *providerFlow) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/upload", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		f.mu.Lock()
		f.uploads++
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"upload_url": "http://" + r.Host + "/audio/9"})
	})
	mux.HandleFunc("/v2/transcript", func(w http.ResponseWriter, r *http.Request) {
		var decoded map[string]any
		_ = json.NewDecoder(r.Body).Decode(&decoded)
		f.mu.Lock()
		f.creates++
		if terms, ok := decoded["keyterms_prompt"].([]any); ok {
			f.keyterms = terms
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "tx-9", "status": "processing"})
	})
	mux.HandleFunc("/v2/transcript/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			f.mu.Lock()
			f.deletes++
			f.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}
		f.mu.Lock()
		f.gets++
		reply := `{"id":"tx-9","status":"processing"}`
		if f.gets <= len(f.replies) {
			reply = f.replies[f.gets-1]
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, reply)
	})
	return mux
}

// wordSchedule returns eight user words on a fixed 2 second schedule with
// the completion and deletion bodies the flow replays.
func wordSchedule() (words []map[string]any, completion, after string) {
	var body strings.Builder
	fmt.Fprintf(&body, `{"id":"tx-9","status":"completed","text":"eight short words follow","audio_duration":2,"words":[`)
	for i := range 8 {
		start := int64(i * 250)
		end := start + 150
		words = append(words, map[string]any{"start": start, "end": end})
		if i > 0 {
			body.WriteString(",")
		}
		fmt.Fprintf(&body, `{"text":"w%d","start":%d,"end":%d,"confidence":0.99,"speaker":null}`, i, start, end)
	}
	body.WriteString("]}")
	completion = body.String()
	after = `{"id":"tx-9","status":"completed","text":"Deleted by user.",` +
		`"audio_url":"http://deleted_by_user","confidence":null,"words":null}`
	return words, completion, after
}

// genClip renders two seconds of 48 kHz mono tone with gaps, so the run
// uploads real audio bytes without a committed fixture or a microphone.
func genClip(t *testing.T) []byte {
	t.Helper()
	const rate = 48000
	pcm := make([]int16, rate*2)
	for i := range pcm {
		second := float64(i) / rate
		if math.Mod(second, 0.25) < 0.15 {
			pcm[i] = int16(9000 * math.Sin(2*math.Pi*440*second))
		}
	}
	buf := new(bytes.Buffer)
	for _, h := range []any{"RIFF", uint32(36 + len(pcm)*2), "WAVE", "fmt ", uint32(16), uint16(1), uint16(1), uint32(rate), uint32(rate * 2), uint16(2), uint16(16), "data", uint32(len(pcm) * 2)} {
		switch v := h.(type) {
		case string:
			buf.WriteString(v)
		case uint32:
			_ = binary.Write(buf, binary.LittleEndian, v)
		case uint16:
			_ = binary.Write(buf, binary.LittleEndian, v)
		}
	}
	for _, s := range pcm {
		_ = binary.Write(buf, binary.LittleEndian, s)
	}
	return buf.Bytes()
}

// TestRunStoresOneTimeline pins the whole pass: budget reserved before the
// call, keyterms from mentions, words merged and stored, receipt kept, the
// provider copy deleted and confirmed, and the reservation settled.
func TestRunStoresOneTimeline(t *testing.T) {
	t.Parallel()
	schedule, completion, after := wordSchedule()
	flow := &providerFlow{replies: []string{`{"id":"tx-9","status":"processing"}`, `{"id":"tx-9","status":"processing"}`, completion, after}}
	server := httptest.NewServer(flow.handler())
	defer server.Close()
	client, err := assemblyai.NewClient(assemblyai.Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	db := openDiary(t)
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "ep-1", "owner-a", 1, "draft")
	addEpisode(t, db, "ep-0", "owner-a", 0, "ready")
	addMention(t, db, "m-1", "owner-a", "ep-0", "Mara")
	addMention(t, db, "m-2", "owner-a", "ep-0", "Quilby")
	budgets := &fakeBudget{}
	var receipt []byte
	result, err := transcript.Run(t.Context(), transcript.Config{
		DB:           db,
		Batch:        client,
		Budgets:      budgets,
		OwnerID:      "owner-a",
		EpisodeID:    "ep-1",
		Audio:        genClip(t),
		DurationSecs: 2,
		Host: []transcript.HostReply{{
			StartMs: 2000,
			Words:   []transcript.HostWord{{Text: "welcome", StartMs: 0, EndMs: 120}},
		}},
		Offsets:      transcript.Offsets{UserMs: 40, HostMs: 60},
		PollInterval: time.Millisecond,
		SaveRaw: func(_ context.Context, raw []byte) error {
			receipt = append([]byte(nil), raw...)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.TranscriptID != "tx-9" || result.Words != 9 {
		t.Fatalf("result = %+v, want tx-9 with 9 words", result)
	}
	stored, err := transcript.Load(t.Context(), db, "ep-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(stored) != 9 {
		t.Fatalf("stored %d words, want 9", len(stored))
	}
	for i, mark := range schedule {
		wantStart := mark["start"].(int64) + 40
		wantEnd := mark["end"].(int64) + 40
		if stored[i].Text != fmt.Sprintf("w%d", i) || stored[i].StartMs != wantStart || stored[i].EndMs != wantEnd {
			t.Fatalf("stored[%d] = %+v, want w%d at %d to %d", i, stored[i], i, wantStart, wantEnd)
		}
	}
	last := stored[8]
	if last.Text != "welcome" || last.StartMs != 2060 || last.EndMs != 2180 {
		t.Fatalf("host word = %+v, want welcome at 2060 to 2180", last)
	}
	if len(receipt) == 0 || !strings.Contains(string(receipt), "tx-9") {
		t.Fatal("receipt is empty or misses the transcript id")
	}
	flow.mu.Lock()
	uploads, creates, deletes := flow.uploads, flow.creates, flow.deletes
	keyterms := append([]any(nil), flow.keyterms...)
	flow.mu.Unlock()
	if uploads != 1 || creates != 1 || deletes != 1 {
		t.Fatalf("provider calls upload=%d create=%d delete=%d, want one each", uploads, creates, deletes)
	}
	if len(keyterms) != 2 || keyterms[0] != "Quilby" || keyterms[1] != "Mara" {
		t.Fatalf("keyterms sent = %v, want both mention names recent first", keyterms)
	}
	budgets.mu.Lock()
	defer budgets.mu.Unlock()
	if len(budgets.reserved) != 1 || len(budgets.settled) != 1 || len(budgets.released) != 0 {
		t.Fatalf("budget holds %+v, want one reserve and one settle", budgets)
	}
	if budgets.settled[0][0] != budgets.reserved[0] {
		t.Fatal("settle does not free the held reservation")
	}
}

// TestWordsLandWithin50ms replays the generated clip schedule and checks
// every stored word lands within 50 ms of its generated boundary.
func TestWordsLandWithin50ms(t *testing.T) {
	t.Parallel()
	schedule, completion, after := wordSchedule()
	flow := &providerFlow{replies: []string{`{"id":"tx-9","status":"processing"}`, `{"id":"tx-9","status":"processing"}`, completion, after}}
	server := httptest.NewServer(flow.handler())
	defer server.Close()
	client, err := assemblyai.NewClient(assemblyai.Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	db := openDiary(t)
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "ep-1", "owner-a", 1, "draft")
	budgets := &fakeBudget{}
	_, err = transcript.Run(t.Context(), transcript.Config{
		DB:           db,
		Batch:        client,
		Budgets:      budgets,
		OwnerID:      "owner-a",
		EpisodeID:    "ep-1",
		Audio:        genClip(t),
		DurationSecs: 2,
		PollInterval: time.Millisecond,
		SaveRaw:      func(context.Context, []byte) error { return nil },
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	stored, err := transcript.Load(t.Context(), db, "ep-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for i, mark := range schedule {
		var startErr, endErr int64
		if startErr = stored[i].StartMs - mark["start"].(int64); startErr < 0 {
			startErr = -startErr
		}
		if endErr = stored[i].EndMs - mark["end"].(int64); endErr < 0 {
			endErr = -endErr
		}
		if startErr > 50 || endErr > 50 {
			t.Fatalf("word %d misses its boundary by %d and %d ms", i, startErr, endErr)
		}
	}
}

// TestRunRefusesWithoutBudget checks a refused reservation reaches no
// provider endpoint and stores no words.
func TestRunRefusesWithoutBudget(t *testing.T) {
	t.Parallel()
	_, completion, after := wordSchedule()
	flow := &providerFlow{replies: []string{`{"id":"tx-9","status":"processing"}`, `{"id":"tx-9","status":"processing"}`, completion, after}}
	server := httptest.NewServer(flow.handler())
	defer server.Close()
	client, err := assemblyai.NewClient(assemblyai.Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	db := openDiary(t)
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "ep-1", "owner-a", 1, "draft")
	budgets := &fakeBudget{refuse: cost.ErrOverBudget}
	_, err = transcript.Run(t.Context(), transcript.Config{
		DB:           db,
		Batch:        client,
		Budgets:      budgets,
		OwnerID:      "owner-a",
		EpisodeID:    "ep-1",
		Audio:        genClip(t),
		DurationSecs: 2,
		PollInterval: time.Millisecond,
		SaveRaw:      func(context.Context, []byte) error { return nil },
	})
	if !errors.Is(err, cost.ErrOverBudget) {
		t.Fatalf("run error = %v, want ErrOverBudget", err)
	}
	flow.mu.Lock()
	uploads, creates := flow.uploads, flow.creates
	flow.mu.Unlock()
	if uploads != 0 || creates != 0 {
		t.Fatalf("provider calls upload=%d create=%d, want none after refusal", uploads, creates)
	}
	stored, err := transcript.Load(t.Context(), db, "ep-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(stored) != 0 {
		t.Fatalf("stored %d words, want none after refusal", len(stored))
	}
	budgets.mu.Lock()
	defer budgets.mu.Unlock()
	if len(budgets.released) != 0 {
		t.Fatal("refusal released a reservation it never held")
	}
}

// TestRunKeepsProviderCopyOnReceiptFailure checks a receipt store failure
// skips the delete, so the provider copy survives for an explicit retry,
// and frees the reservation.
func TestRunKeepsProviderCopyOnReceiptFailure(t *testing.T) {
	t.Parallel()
	_, completion, after := wordSchedule()
	flow := &providerFlow{replies: []string{`{"id":"tx-9","status":"processing"}`, `{"id":"tx-9","status":"processing"}`, completion, after}}
	server := httptest.NewServer(flow.handler())
	defer server.Close()
	client, err := assemblyai.NewClient(assemblyai.Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	db := openDiary(t)
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "ep-1", "owner-a", 1, "draft")
	budgets := &fakeBudget{}
	receiptErr := errors.New("disk full")
	_, err = transcript.Run(t.Context(), transcript.Config{
		DB:           db,
		Batch:        client,
		Budgets:      budgets,
		OwnerID:      "owner-a",
		EpisodeID:    "ep-1",
		Audio:        genClip(t),
		DurationSecs: 2,
		PollInterval: time.Millisecond,
		SaveRaw:      func(context.Context, []byte) error { return receiptErr },
	})
	if !errors.Is(err, receiptErr) {
		t.Fatalf("run error = %v, want the receipt failure", err)
	}
	if !strings.Contains(err.Error(), "tx-9") {
		t.Fatalf("run error = %v, want it to name tx-9 for the explicit retry", err)
	}
	flow.mu.Lock()
	deletes := flow.deletes
	flow.mu.Unlock()
	if deletes != 0 {
		t.Fatal("run deleted the provider copy before persisting the receipt")
	}
	budgets.mu.Lock()
	defer budgets.mu.Unlock()
	if len(budgets.settled) != 0 || len(budgets.released) != 1 {
		t.Fatalf("budget holds %+v, want one release and no settle", budgets)
	}
}

// TestRunConfirmsDelete checks a provider copy that stays readable after
// the delete call fails the run instead of settling.
func TestRunConfirmsDelete(t *testing.T) {
	t.Parallel()
	_, completion, _ := wordSchedule()
	flow := &providerFlow{replies: []string{`{"id":"tx-9","status":"processing"}`, `{"id":"tx-9","status":"processing"}`, completion, completion}}
	server := httptest.NewServer(flow.handler())
	defer server.Close()
	client, err := assemblyai.NewClient(assemblyai.Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	db := openDiary(t)
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "ep-1", "owner-a", 1, "draft")
	budgets := &fakeBudget{}
	_, err = transcript.Run(t.Context(), transcript.Config{
		DB:           db,
		Batch:        client,
		Budgets:      budgets,
		OwnerID:      "owner-a",
		EpisodeID:    "ep-1",
		Audio:        genClip(t),
		DurationSecs: 2,
		PollInterval: time.Millisecond,
		SaveRaw:      func(context.Context, []byte) error { return nil },
	})
	if !errors.Is(err, transcript.ErrDeleteUnconfirmed) {
		t.Fatalf("run error = %v, want ErrDeleteUnconfirmed", err)
	}
	budgets.mu.Lock()
	defer budgets.mu.Unlock()
	if len(budgets.settled) != 0 {
		t.Fatal("run settled spend while a provider copy lingers")
	}
}

// TestEstimatePricesBatchRate checks one audio hour costs the batch rate
// and one second rounds up to the nanodollar.
func TestEstimatePricesBatchRate(t *testing.T) {
	t.Parallel()
	if got := transcript.Estimate(3600); got != cost.USD(0.21) {
		t.Fatalf("hour estimate = %v, want the batch hourly rate", got)
	}
	if got := transcript.Estimate(1); got != cost.Price(58334) {
		t.Fatalf("second estimate = %v, want 58334 nanodollars", got)
	}
	if got := transcript.Estimate(0); got != cost.Price(0) {
		t.Fatalf("zero estimate = %v, want nothing", got)
	}
}

// TestKindNeverResumes pins the job kind: two at once, never idempotent,
// no resume path, one attempt. A restart marks the job interrupted and no
// path repays the call on its own.
func TestKindNeverResumes(t *testing.T) {
	t.Parallel()
	kind := transcript.Kind
	if kind.Limit != 2 {
		t.Fatalf("kind limit = %d, want 2", kind.Limit)
	}
	if kind.Idempotent {
		t.Fatal("kind is idempotent, which would repay a restart")
	}
	if kind.Resume != nil {
		t.Fatal("kind carries a resume path for a paid call")
	}
	if kind.MaxAttempts != 0 {
		t.Fatalf("kind max attempts = %d, want one attempt", kind.MaxAttempts)
	}
}
