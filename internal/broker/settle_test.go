package broker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nrynss/reprise/internal/assemblyai"
)

// mintLinked starts one session through the broker and returns its ids.
// The mint records the settle linkage, so the end path can rebuild the
// reconciler input from the diary session id alone.
func mintLinked(t *testing.T, fx *fixture) sessionBody {
	t.Helper()
	got := fx.post(nil)
	if got.rec.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201: %s", got.rec.Code, got.rec.Body.String())
	}
	var body sessionBody
	if err := json.Unmarshal(got.rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	return body
}

// TestMintRecordsSettleLinkage checks a successful mint stores the lease
// and reservation the end path needs. A mint without a recorded link
// can never settle, so the link lands on the success path or the mint
// fails outright.
func TestMintRecordsSettleLinkage(t *testing.T) {
	fx := newFixture(t, nil)
	body := mintLinked(t, fx)
	var leaseID, raw string
	var cap int
	err := fx.db.Reader().QueryRowContext(t.Context(),
		`SELECT lease_id, reservation, token_cap FROM session_settle WHERE session_id = ?`,
		body.SessionID).Scan(&leaseID, &raw, &cap)
	if err != nil {
		t.Fatalf("read linkage: %v", err)
	}
	if leaseID == "" {
		t.Fatal("linkage holds no lease id")
	}
	var reservation struct {
		ID string `json:"ID"`
	}
	if err := json.Unmarshal([]byte(raw), &reservation); err != nil {
		t.Fatalf("decode reservation: %v", err)
	}
	if reservation.ID == "" {
		t.Fatalf("linkage holds no reservation: %s", raw)
	}
	if cap != testCapSeconds {
		t.Fatalf("linkage cap %d, want %d", cap, testCapSeconds)
	}
}

// TestSettleInputNeedsProviderClose checks the end input waits for the
// recorded close. Settling silence would price nothing and free the
// wrong hold, so the caller waits for the end or the sweep instead.
func TestSettleInputNeedsProviderClose(t *testing.T) {
	fx := newFixture(t, nil)
	body := mintLinked(t, fx)
	if _, err := fx.broker.SettleInput(t.Context(), body.SessionID); !errors.Is(err, ErrNoClose) {
		t.Fatalf("settle input err %v, want ErrNoClose", err)
	}
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`UPDATE sessions SET provider_session_id = ? WHERE id = ?`, "prov-end-1", body.SessionID); err != nil {
		t.Fatalf("record close: %v", err)
	}
	in, err := fx.broker.SettleInput(t.Context(), body.SessionID)
	if err != nil {
		t.Fatalf("settle input: %v", err)
	}
	if in.SessionID != body.SessionID || in.EpisodeID != body.EpisodeID {
		t.Fatalf("input names %s/%s, want %s/%s", in.SessionID, in.EpisodeID, body.SessionID, body.EpisodeID)
	}
	if in.ProviderSessionID != "prov-end-1" || in.LeaseID == "" || in.Reservation.ID == "" {
		t.Fatalf("input misses linkage: %+v", in)
	}
	if in.TokenCapSeconds != testCapSeconds {
		t.Fatalf("input cap %d, want %d", in.TokenCapSeconds, testCapSeconds)
	}
	if _, err := fx.broker.SettleInput(t.Context(), "no-such-session"); !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("unknown session err %v, want ErrUnknownSession", err)
	}
}

// TestListOpenIncludesUnclosedRows checks the sweep source lists rows
// with and without a recorded provider close, oldest first. A row the
// browser never reported still bills its call, so the sweep must see it
// instead of leaving its hold to expire with no charge.
func TestListOpenIncludesUnclosedRows(t *testing.T) {
	fx := newFixture(t, nil)
	open := mintLinked(t, fx)
	closed := mintLinked(t, fx)
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`UPDATE sessions SET provider_session_id = ? WHERE id = ?`, "prov-open-1", closed.SessionID); err != nil {
		t.Fatalf("record close: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`UPDATE session_settle SET minted_at = ? WHERE session_id = ?`,
		time.Now().Add(-90*time.Second).UnixMilli(), closed.SessionID); err != nil {
		t.Fatalf("age link: %v", err)
	}
	listed, err := fx.broker.ListOpen(t.Context())
	if err != nil {
		t.Fatalf("list open: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("listed %d candidates, want 2", len(listed))
	}
	first := listed[0]
	if first.SessionID != closed.SessionID || first.ProviderSessionID != "prov-open-1" {
		t.Fatalf("first candidate is %+v, want the closed session", first)
	}
	if first.OpenSeconds < 80 || first.OpenSeconds > 3600 {
		t.Fatalf("first candidate age %d, want about 90 seconds", first.OpenSeconds)
	}
	if first.Reservation.ID == "" || first.LeaseID == "" || first.TokenCapSeconds != testCapSeconds {
		t.Fatalf("first candidate misses linkage: %+v", first)
	}
	second := listed[1]
	if second.SessionID != open.SessionID || second.ProviderSessionID != "" {
		t.Fatalf("second candidate is %+v, want the unclosed session with an empty provider id", second)
	}
	if second.Reservation.ID == "" || second.LeaseID == "" || second.TokenCapSeconds != testCapSeconds {
		t.Fatalf("second candidate misses linkage: %+v", second)
	}
}

// sessionDoc answers one provider session body the way the Sessions API
// shapes it: flat duration, artifacts by type, start time behind it.
func sessionDoc(id string, duration *float64, artifacts string) string {
	body := `{"id":"` + id + `","status":"completed","public_close_reason":"client end","created_at":"2026-09-19T10:00:00Z","ended_at":"2026-09-19T10:05:00Z"`
	if duration != nil {
		body += `,"duration_seconds":` + jsonNumber(*duration)
	}
	return body + `,"artifacts":[` + artifacts + `]}`
}

func jsonNumber(f float64) string {
	raw, _ := json.Marshal(f)
	return string(raw)
}

// sessionsServer answers provider session reads and deletes from fixed
// bodies. Missing ids answer 404, the way a deleted record reads.
func sessionsServer(t *testing.T, docs map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/v1/sessions/"
		if !strings.HasPrefix(r.URL.Path, prefix) || len(r.URL.Path) == len(prefix) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		id := r.URL.Path[len(prefix):]
		switch r.Method {
		case http.MethodGet:
			doc, ok := docs[id]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(doc))
		case http.MethodDelete:
			if _, ok := docs[id]; !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
}

func testAdapter(t *testing.T, srv *httptest.Server) *SessionsAdapter {
	t.Helper()
	client, err := assemblyai.NewSessionsClient(srv.URL, "probe-key", srv.Client())
	if err != nil {
		t.Fatalf("new sessions client: %v", err)
	}
	adapter, err := NewSessionsAdapter(client)
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	return adapter
}

// TestAdapterReadsClosedSession checks the reconcile read maps a closed
// record: the duration rounds up to whole seconds, and the audio and
// timeline artifacts ride along for the persist.
func TestAdapterReadsClosedSession(t *testing.T) {
	duration := 372.4
	srv := sessionsServer(t, map[string]string{
		"prov-read": sessionDoc("prov-read", &duration,
			`{"type":"audio","url":"https://artifacts.example/rec.ogg","content_type":"audio/ogg"},`+
				`{"type":"timeline","url":"https://artifacts.example/tl.json","content_type":"application/json"}`),
	})
	defer srv.Close()
	read, err := testAdapter(t, srv).ReadSession(context.Background(), "prov-read")
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	if read.DurationSeconds != 373 {
		t.Fatalf("duration %d, want 373 from 372.4 rounded up", read.DurationSeconds)
	}
	if read.RecordingURL != "https://artifacts.example/rec.ogg" {
		t.Fatalf("recording %q, want the audio artifact", read.RecordingURL)
	}
	if read.TimelineURL != "https://artifacts.example/tl.json" {
		t.Fatalf("timeline %q, want the timeline artifact", read.TimelineURL)
	}
}

// TestAdapterRefusesOpenAndGoneReads checks the reconcile read fails
// where no duration exists yet or any more. Pricing either would
// invent spend, so both report ErrRead and the caller retries or
// reviews instead.
func TestAdapterRefusesOpenAndGoneReads(t *testing.T) {
	srv := sessionsServer(t, map[string]string{
		"prov-open": `{"id":"prov-open","status":"created","created_at":"2026-09-19T10:00:00Z","artifacts":[]}`,
	})
	defer srv.Close()
	adapter := testAdapter(t, srv)
	_, err := adapter.ReadSession(context.Background(), "prov-open")
	if !errors.Is(err, ErrSessionOpen) || !errors.Is(err, ErrRead) {
		t.Fatalf("open read err %v, want ErrSessionOpen wrapping ErrRead", err)
	}
	_, err = adapter.ReadSession(context.Background(), "prov-gone")
	if !errors.Is(err, ErrRead) || errors.Is(err, ErrSessionOpen) {
		t.Fatalf("gone read err %v, want ErrRead and not ErrSessionOpen", err)
	}
}

// TestAdapterReadsOpenStatus checks the sweep status maps an open
// record to elapsed time since the provider opened it. The sweep books
// the accrued cost from that age, never from a missing duration.
func TestAdapterReadsOpenStatus(t *testing.T) {
	srv := sessionsServer(t, map[string]string{
		"prov-live": `{"id":"prov-live","status":"created","created_at":"2026-09-19T10:00:00Z","artifacts":[]}`,
	})
	defer srv.Close()
	status, err := testAdapter(t, srv).ReadStatus(context.Background(), "prov-live")
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status.HasDuration {
		t.Fatalf("open status %+v, want no duration", status)
	}
	if status.OpenSeconds <= 0 {
		t.Fatalf("open age %d, want positive seconds since 2026", status.OpenSeconds)
	}
	if _, err := testAdapter(t, srv).ReadStatus(context.Background(), "prov-gone"); !errors.Is(err, ErrProviderGone) {
		t.Fatalf("gone status err %v, want ErrProviderGone", err)
	}
}

// TestAdapterEndsSession checks the sweep delete maps both provider
// answers. A removed record and one already gone both succeed, because
// ending an ended session stays safe. The voice socket is already
// closed, so this measures the delete.
func TestAdapterEndsSession(t *testing.T) {
	srv := sessionsServer(t, map[string]string{"prov-end": sessionDoc("prov-end", nil, "")})
	defer srv.Close()
	client, err := assemblyai.NewSessionsClient(srv.URL, "probe-key", srv.Client())
	if err != nil {
		t.Fatalf("new sessions client: %v", err)
	}
	for _, id := range []string{"prov-end", "prov-gone"} {
		if err := client.HoldSocket(id, closedSocket{}); err != nil {
			t.Fatalf("hold socket: %v", err)
		}
	}
	adapter, err := NewSessionsAdapter(client)
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	ended, err := adapter.EndSession(context.Background(), "prov-end")
	if err != nil {
		t.Fatalf("end session: %v", err)
	}
	if !ended.Deleted {
		t.Fatalf("end result %+v, want deleted", ended)
	}
	gone, err := adapter.EndSession(context.Background(), "prov-gone")
	if err != nil {
		t.Fatalf("end gone session: %v", err)
	}
	if gone.Deleted {
		t.Fatalf("gone result %+v, want deleted false with success", gone)
	}
}

// closedSocket is a voice socket that has already gone. The end still
// deletes the provider record and does not open a replacement.
type closedSocket struct{}

func (closedSocket) SendText(context.Context, []byte) error {
	return assemblyai.ErrSocketGone
}

// TestAdapterRefusesNilClient checks a missing client fails before any
// provider call, so wiring faults surface instead of settling nothing.
func TestAdapterRefusesNilClient(t *testing.T) {
	if _, err := NewSessionsAdapter(nil); err == nil {
		t.Fatal("nil client built without error")
	}
	adapter := &SessionsAdapter{}
	if _, err := adapter.ReadSession(context.Background(), "x"); err == nil {
		t.Fatal("read without client succeeded")
	}
	if _, err := adapter.ReadStatus(context.Background(), "x"); err == nil {
		t.Fatal("status without client succeeded")
	}
	if _, err := adapter.EndSession(context.Background(), "x"); err == nil {
		t.Fatal("end without client succeeded")
	}
}
