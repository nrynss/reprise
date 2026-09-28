package broker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/nrynss/reprise/internal/assemblyai"
)

// orderSocket records one end frame for the adapter test.
type orderSocket struct {
	mu    *sync.Mutex
	order *[]string
	sent  [][]byte
}

func (s *orderSocket) SendText(_ context.Context, payload []byte) error {
	s.sent = append(s.sent, append([]byte(nil), payload...))
	s.mu.Lock()
	*s.order = append(*s.order, "end")
	s.mu.Unlock()
	return nil
}

// TestAdapterEndsLiveSocketBeforeDelete checks the sweep end path writes
// session.end before the provider delete.
func TestAdapterEndsLiveSocketBeforeDelete(t *testing.T) {
	var mu sync.Mutex
	var order []string
	recorded := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/v1/sessions/prov-live" {
			mu.Lock()
			order = append(order, "delete")
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer recorded.Close()
	client, err := assemblyai.NewSessionsClient(recorded.URL, "probe-key", recorded.Client())
	if err != nil {
		t.Fatalf("new sessions client: %v", err)
	}
	sock := &orderSocket{mu: &mu, order: &order}
	if err := client.HoldSocket("prov-live", sock); err != nil {
		t.Fatalf("hold socket: %v", err)
	}
	adapter, err := NewSessionsAdapter(client)
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	res, err := adapter.EndSession(t.Context(), "prov-live")
	if err != nil {
		t.Fatalf("end session: %v", err)
	}
	if !res.SocketEnded || !res.Deleted {
		t.Fatalf("end result %+v, want the frame and the delete", res)
	}
	if len(sock.sent) != 1 || string(sock.sent[0]) != `{"type":"session.end"}` {
		t.Fatalf("socket frames %q, want one session.end", sock.sent)
	}
	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()
	if len(got) != 2 || got[0] != "end" || got[1] != "delete" {
		t.Fatalf("order %v, want end then delete", got)
	}
}

// openThenClosed is a provider read that stays open until the socket
// stops, then reports a duration. It also records the delete.
type openThenClosed struct {
	mu      sync.Mutex
	log     []string
	stopped bool
}

func (o *openThenClosed) ReadSession(context.Context, string) (ProviderSession, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.log = append(o.log, "read")
	if !o.stopped {
		return ProviderSession{}, ErrSessionOpen
	}
	return ProviderSession{
		ID:              "prov-open",
		DurationSeconds: 12,
		RecordingURL:    "https://artifacts.example/rec-a.ogg",
		TimelineURL:     "https://artifacts.example/tl-a.json",
	}, nil
}

func (o *openThenClosed) StopSocket(context.Context, string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stopped = true
	o.log = append(o.log, "stop")
	return nil
}

func (o *openThenClosed) EndSession(context.Context, string) (EndResult, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.log = append(o.log, "end")
	return EndResult{Deleted: true, SocketEnded: true, Detail: "socket ended, record deleted"}, nil
}

func (o *openThenClosed) steps() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.log...)
}

// TestReconcileStopsOpenSessionThenSettles checks reconcile ends a live
// socket before it prices, and deletes only after that read. A second
// pass settles nothing new and does not stop again.
func TestReconcileStopsOpenSessionThenSettles(t *testing.T) {
	fx := newRecFixture(t)
	in := fx.mintSession("prov-open")
	reader := &openThenClosed{}
	rec, err := NewReconciler(ReconcilerConfig{
		DB:       fx.db,
		Sessions: reader,
		Artifacts: recFetcher{blobs: map[string][]byte{
			"https://artifacts.example/rec-a.ogg": recAudioA,
			"https://artifacts.example/tl-a.json": recTimelineA,
		}},
		Budgets:              fx.budgets,
		Leases:               fx.leases,
		Diary:                fx.diary,
		Media:                fx.media,
		Alerter:              fx.alerter,
		MarginSeconds:        DefaultMarginSeconds,
		RecordingContentType: DefaultRecordingContentType,
		FetchMaxBytes:        DefaultFetchMaxBytes,
		Wait:                 func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatalf("new reconciler: %v", err)
	}
	res, err := rec.Reconcile(t.Context(), in)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.ConnectedSeconds != 12 {
		t.Fatalf("settled %d seconds, want 12", res.ConnectedSeconds)
	}
	steps := reader.steps()
	if len(steps) != 4 || steps[0] != "read" || steps[1] != "stop" || steps[2] != "read" || steps[3] != "end" {
		t.Fatalf("steps %v, want read, stop, read, end", steps)
	}
	if got := recSpent(t, fx.costs); got != recRate*12 {
		t.Fatalf("spent %d, want %d", got, recRate*12)
	}
	media := recMediaTypes(t, fx.db, in.EpisodeID)
	if media["audio/ogg"] != 1 || media["application/json"] != 1 {
		t.Fatalf("media %v, want one recording and one timeline", media)
	}
	again, err := rec.Reconcile(t.Context(), in)
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if again.ConnectedSeconds != 12 || again.RecordingMediaID != res.RecordingMediaID || again.TimelineMediaID != res.TimelineMediaID {
		t.Fatalf("second result %+v, want the same stored ids", again)
	}
	if got := recSpent(t, fx.costs); got != recRate*12 {
		t.Fatalf("spent moved to %d on the second pass", got)
	}
	media = recMediaTypes(t, fx.db, in.EpisodeID)
	if media["audio/ogg"] != 1 || media["application/json"] != 1 {
		t.Fatalf("media %v after the second pass, want one of each", media)
	}
	steps = reader.steps()
	if len(steps) != 5 || steps[4] != "end" {
		t.Fatalf("steps %v, want one more end and no second stop", steps)
	}
}

// TestSweepRecordsSocketEnd checks the sweep stores that the end wrote
// session.end. A delete with no frame leaves the flag clear.
func TestSweepRecordsSocketEnd(t *testing.T) {
	sf := newSweepFixture(t, 4000)
	sf.statuses.docs["prov-sweep"] = ProviderStatus{
		ID: "prov-sweep", Status: "completed", HasDuration: true, DurationSeconds: 10,
	}
	sf.sweeper.ender = socketFlagEnder{}
	out, err := sf.sweeper.Sweep(t.Context(), SweepInput{MarginSeconds: DefaultMarginSeconds})
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(out) != 1 || !out[0].ServerEnded {
		t.Fatalf("sweep outcome %+v, want the socket marked ended", out)
	}
	_, _, ended, _ := sweepRow(t, sf.fx.db, sf.candidate.SessionID)
	if ended != 1 {
		t.Fatalf("sweep row server_ended %d, want 1", ended)
	}
}

// socketFlagEnder reports that the end frame was written.
type socketFlagEnder struct{}

func (socketFlagEnder) EndSession(context.Context, string) (EndResult, error) {
	return EndResult{Deleted: true, SocketEnded: true, Detail: "socket ended, record deleted"}, nil
}
