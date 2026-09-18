package broker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nrynss/keel/cost"
	"github.com/nrynss/keel/job"
	"github.com/nrynss/keel/sqlite"
)

// sweepSource serves a fixed candidate list. The store owner replaces it
// with the diary listing once the lease link lands on the session rows.
type sweepSource struct {
	listed []Candidate
	err    error
}

func (s sweepSource) ListOpen(_ context.Context) ([]Candidate, error) {
	return s.listed, s.err
}

// sweepStatuses serves fixed provider statuses. Gone ids report
// ErrProviderGone, the way a deleted record reads.
type sweepStatuses struct {
	docs map[string]ProviderStatus
	gone map[string]bool
}

func (s sweepStatuses) ReadStatus(_ context.Context, providerSessionID string) (ProviderStatus, error) {
	if s.gone[providerSessionID] {
		return ProviderStatus{}, ErrProviderGone
	}
	doc, ok := s.docs[providerSessionID]
	if !ok {
		return ProviderStatus{}, errors.New("sweepStatuses: unknown session " + providerSessionID)
	}
	return doc, nil
}

// sweepEnder records every delete. Ending repeats safely, so it never
// fails a repeated id.
type sweepEnder struct {
	calls []string
}

func (e *sweepEnder) EndSession(_ context.Context, providerSessionID string) (EndResult, error) {
	e.calls = append(e.calls, providerSessionID)
	return EndResult{Deleted: true, Detail: "record deleted"}, nil
}

type sweepFixture struct {
	fx        *recFixture
	source    *sweepSource
	statuses  *sweepStatuses
	ender     *sweepEnder
	sweeper   *Sweeper
	current   *time.Time
	candidate Candidate
}

// newSweepFixture mints one session under a fake clock, then moves the
// clock a second past a 150 ms lease cap, so the lease reads expired the
// way a production sweep past cap plus margin meets it. No test sleeps.
func newSweepFixture(t *testing.T, openSeconds int) *sweepFixture {
	t.Helper()
	start := time.Now()
	current := start
	fx := newRecFixtureClock(t, func() time.Time { return current }, 150*time.Millisecond)
	in := fx.mintSession("prov-sweep")
	current = start.Add(time.Second)
	source := &sweepSource{}
	statuses := &sweepStatuses{docs: map[string]ProviderStatus{}, gone: map[string]bool{}}
	ender := &sweepEnder{}
	sweeper, err := NewSweeper(SweeperConfig{
		DB:            fx.db,
		Source:        source,
		Statuses:      statuses,
		Ender:         ender,
		Reconciler:    fx.rec,
		Alerter:       fx.alerter,
		MarginSeconds: DefaultMarginSeconds,
		Now:           func() time.Time { return current },
	})
	if err != nil {
		t.Fatalf("new sweeper: %v", err)
	}
	candidate := Candidate{
		SessionID:         in.SessionID,
		OwnerID:           in.OwnerID,
		EpisodeID:         in.EpisodeID,
		ProviderSessionID: in.ProviderSessionID,
		LeaseID:           in.LeaseID,
		Reservation:       in.Reservation,
		TokenCapSeconds:   in.TokenCapSeconds,
		OpenSeconds:       openSeconds,
	}
	source.listed = []Candidate{candidate}
	return &sweepFixture{fx: fx, source: source, statuses: statuses, ender: ender,
		sweeper: sweeper, current: &current, candidate: candidate}
}

func sweepOwnerSpent(t *testing.T, db *sqlite.DB, owner string) cost.Price {
	t.Helper()
	var spent int64
	if err := db.Reader().QueryRowContext(t.Context(),
		`SELECT spent_nd FROM cost_owner_budget WHERE owner = ?`, owner).Scan(&spent); err != nil {
		t.Fatalf("read owner spent: %v", err)
	}
	return cost.Price(spent)
}

func sweepRow(t *testing.T, db *sqlite.DB, sessionID string) (string, int, int, string) {
	t.Helper()
	var status, detail string
	var seconds, ended int
	if err := db.Reader().QueryRowContext(t.Context(),
		`SELECT provider_status, connected_seconds, server_ended, detail FROM sweep_state WHERE session_id = ?`,
		sessionID).Scan(&status, &seconds, &ended, &detail); err != nil {
		t.Fatalf("read sweep row: %v", err)
	}
	return status, seconds, ended, detail
}

func TestSweepSettlesCompletedPastCapExactlyOnce(t *testing.T) {
	sf := newSweepFixture(t, 4000)
	sf.statuses.docs["prov-sweep"] = ProviderStatus{
		ID: "prov-sweep", Status: "completed", CloseReason: "client end",
		HasDuration: true, DurationSeconds: 3600,
		RecordingURL: "https://artifacts.example/rec-a.ogg",
		TimelineURL:  "https://artifacts.example/tl-a.json",
	}
	sf.fx.rec.artifacts = recFetcher{blobs: map[string][]byte{
		"https://artifacts.example/rec-a.ogg": recAudioA,
	}}

	out, err := sf.sweeper.Sweep(t.Context(), SweepInput{MarginSeconds: DefaultMarginSeconds})
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(out) != 1 || out[0].Skipped || out[0].ConnectedSeconds != 3600 {
		t.Fatalf("sweep outcome %+v, want one settled 3600 second session", out)
	}
	if out[0].ServerEnded {
		t.Fatalf("sweep outcome %+v claims a server end no call provides", out[0])
	}
	if len(sf.ender.calls) != 1 || sf.ender.calls[0] != "prov-sweep" {
		t.Fatalf("ender calls %v, want exactly the swept session", sf.ender.calls)
	}
	// The real cost doubles the reservation. The shortfall lands on both
	// ceilings and nothing stays held.
	if got := recSpent(t, sf.fx.costs); got != recRate*3600 {
		t.Fatalf("global spent %d, want %d", got, recRate*3600)
	}
	if got := sweepOwnerSpent(t, sf.fx.db, recOwner); got != recRate*3600 {
		t.Fatalf("owner spent %d, want %d", got, recRate*3600)
	}
	if held := recReserved(t, sf.fx.costs); held != 0 {
		t.Fatalf("held %d, want none held", held)
	}
	if recConnected(t, sf.fx.db, sf.candidate.SessionID) != 3600 {
		t.Fatalf("session row missed its duration")
	}
	over := sf.fx.alerter.ofKind(AlertOverCap)
	if len(over) != 1 || over[0].SessionID != sf.candidate.SessionID {
		t.Fatalf("over cap alerts %v, want exactly this session", sf.fx.alerter.alerts)
	}
	status, seconds, ended, detail := sweepRow(t, sf.fx.db, sf.candidate.SessionID)
	if status != "completed" || seconds != 3600 || ended != 0 || detail == "" {
		t.Fatalf("sweep row %q/%d/%d/%q is wrong", status, seconds, ended, detail)
	}

	again, err := sf.sweeper.Sweep(t.Context(), SweepInput{MarginSeconds: DefaultMarginSeconds})
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if len(again) != 1 || !again[0].Skipped {
		t.Fatalf("second sweep %+v, want one skipped outcome", again)
	}
	if len(sf.ender.calls) != 1 {
		t.Fatalf("ender calls %v after resweep, want no second delete", sf.ender.calls)
	}
	if got := recSpent(t, sf.fx.costs); got != recRate*3600 {
		t.Fatalf("global spent moved to %d on resweep, want no second settle", got)
	}
	if got := sweepOwnerSpent(t, sf.fx.db, recOwner); got != recRate*3600 {
		t.Fatalf("owner spent moved to %d on resweep, want no second settle", got)
	}
	if len(sf.fx.alerter.alerts) != 1 {
		t.Fatalf("alerts %v after resweep, want no second alert", sf.fx.alerter.alerts)
	}
}

func TestSweepSettlesOpenSessionAtElapsed(t *testing.T) {
	sf := newSweepFixture(t, 4000)
	sf.statuses.docs["prov-sweep"] = ProviderStatus{
		ID: "prov-sweep", Status: "created", HasDuration: false, OpenSeconds: 3600,
	}

	out, err := sf.sweeper.Sweep(t.Context(), SweepInput{MarginSeconds: DefaultMarginSeconds})
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(out) != 1 || out[0].Skipped || out[0].ConnectedSeconds != 3600 {
		t.Fatalf("sweep outcome %+v, want one settled 3600 second session", out)
	}
	if got := recSpent(t, sf.fx.costs); got != recRate*3600 {
		t.Fatalf("global spent %d, want the elapsed cost %d", got, recRate*3600)
	}
	if got := sweepOwnerSpent(t, sf.fx.db, recOwner); got != recRate*3600 {
		t.Fatalf("owner spent %d, want the elapsed cost %d", got, recRate*3600)
	}
	if held := recReserved(t, sf.fx.costs); held != 0 {
		t.Fatalf("held %d, want none held", held)
	}
	open := sf.fx.alerter.ofKind(AlertSweepOpen)
	if len(open) != 1 || open[0].SessionID != sf.candidate.SessionID {
		t.Fatalf("sweep open alerts %v, want exactly this session", sf.fx.alerter.alerts)
	}
	if len(sf.ender.calls) != 1 {
		t.Fatalf("ender calls %v, want the record deleted after the settle", sf.ender.calls)
	}
	status, seconds, ended, _ := sweepRow(t, sf.fx.db, sf.candidate.SessionID)
	if status != "created" || seconds != 3600 || ended != 0 {
		t.Fatalf("sweep row %q/%d/%d is wrong", status, seconds, ended)
	}

	if _, err := sf.sweeper.Sweep(t.Context(), SweepInput{MarginSeconds: DefaultMarginSeconds}); err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if len(sf.ender.calls) != 1 {
		t.Fatalf("ender calls %v after resweep, want no second delete", sf.ender.calls)
	}
	if got := recSpent(t, sf.fx.costs); got != recRate*3600 {
		t.Fatalf("global spent moved to %d on resweep", got)
	}
	if len(sf.fx.alerter.alerts) != 2 {
		t.Fatalf("alerts %v after resweep, want no new alert", sf.fx.alerter.alerts)
	}
}

// failAlerter drops every alert with a fixed error. The settle stands
// regardless, and the sweep row keeps the real outcome beside the note.
type failAlerter struct {
	err error
}

func (a failAlerter) Report(context.Context, Alert) error {
	return a.err
}

func TestSweepAlertFailureKeepsOutcome(t *testing.T) {
	sf := newSweepFixture(t, 4000)
	sf.statuses.docs["prov-sweep"] = ProviderStatus{
		ID: "prov-sweep", Status: "created", HasDuration: false, OpenSeconds: 3600,
	}
	sf.sweeper.alerter = failAlerter{err: errors.New("delivery down")}

	out, err := sf.sweeper.Sweep(t.Context(), SweepInput{MarginSeconds: DefaultMarginSeconds})
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(out) != 1 || out[0].Skipped || out[0].ConnectedSeconds != 3600 {
		t.Fatalf("sweep outcome %+v, want one settled 3600 second session", out)
	}
	if got := recSpent(t, sf.fx.costs); got != recRate*3600 {
		t.Fatalf("global spent %d, want the elapsed cost %d", got, recRate*3600)
	}
	status, seconds, _, detail := sweepRow(t, sf.fx.db, sf.candidate.SessionID)
	if status != "created" || seconds != 3600 {
		t.Fatalf("sweep row %q/%d/%q, want created/3600 with the alert note", status, seconds, detail)
	}
	if !strings.Contains(detail, "alert failed: delivery down") {
		t.Fatalf("sweep row detail %q misses the alert failure note", detail)
	}
}

func TestSweepSkipsYoung(t *testing.T) {
	sf := newSweepFixture(t, 100)
	sf.statuses.docs["prov-sweep"] = ProviderStatus{
		ID: "prov-sweep", Status: "completed", HasDuration: true, DurationSeconds: 100,
	}

	out, err := sf.sweeper.Sweep(t.Context(), SweepInput{MarginSeconds: DefaultMarginSeconds})
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(out) != 1 || !out[0].Skipped {
		t.Fatalf("sweep outcome %+v, want one skipped session", out)
	}
	if len(sf.ender.calls) != 0 {
		t.Fatalf("ender calls %v for a young session, want none", sf.ender.calls)
	}
	if got := recSpent(t, sf.fx.costs); got != 0 {
		t.Fatalf("spent %d on a young session, want zero", got)
	}
	if held := recReserved(t, sf.fx.costs); held != sf.fx.estimate {
		t.Fatalf("held %d, want the untouched estimate", held)
	}
}

func TestSweepSkipsSettled(t *testing.T) {
	sf := newSweepFixture(t, 4000)
	sf.statuses.docs["prov-sweep"] = ProviderStatus{
		ID: "prov-sweep", Status: "completed", HasDuration: true, DurationSeconds: 10,
	}
	in := Input{
		SessionID: sf.candidate.SessionID, OwnerID: sf.candidate.OwnerID,
		EpisodeID: sf.candidate.EpisodeID, ProviderSessionID: "prov-short",
		LeaseID: sf.candidate.LeaseID, Reservation: sf.candidate.Reservation,
		TokenCapSeconds: recCapSeconds,
	}
	if _, err := sf.fx.rec.Reconcile(t.Context(), in); err != nil {
		t.Fatalf("pre-settle: %v", err)
	}
	spent := recSpent(t, sf.fx.costs)

	out, err := sf.sweeper.Sweep(t.Context(), SweepInput{MarginSeconds: DefaultMarginSeconds})
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(out) != 1 || !out[0].Skipped {
		t.Fatalf("sweep outcome %+v, want one skipped session", out)
	}
	if len(sf.ender.calls) != 0 {
		t.Fatalf("ender calls %v for a settled session, want none", sf.ender.calls)
	}
	if got := recSpent(t, sf.fx.costs); got != spent {
		t.Fatalf("spent moved %d to %d, want no second settle", spent, got)
	}
}

func TestSweepGoneRecordReviews(t *testing.T) {
	sf := newSweepFixture(t, 4000)
	sf.statuses.gone["prov-sweep"] = true

	out, err := sf.sweeper.Sweep(t.Context(), SweepInput{MarginSeconds: DefaultMarginSeconds})
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(out) != 1 || out[0].Skipped || out[0].ProviderStatus != "gone" {
		t.Fatalf("sweep outcome %+v, want one reviewed session", out)
	}
	if got := recSpent(t, sf.fx.costs); got != 0 {
		t.Fatalf("spent %d on an unknowable duration, want zero", got)
	}
	if held := recReserved(t, sf.fx.costs); held != sf.fx.estimate {
		t.Fatalf("held %d, want the untouched estimate", held)
	}
	reviews := sf.fx.alerter.ofKind(AlertNeedsReview)
	if len(reviews) != 1 || reviews[0].SessionID != sf.candidate.SessionID {
		t.Fatalf("review alerts %v, want exactly this session", sf.fx.alerter.alerts)
	}
	if len(sf.ender.calls) != 0 {
		t.Fatalf("ender calls %v for a gone record, want none", sf.ender.calls)
	}
	if _, _, _, detail := sweepRow(t, sf.fx.db, sf.candidate.SessionID); detail == "" {
		t.Fatalf("sweep row missed its detail")
	}
}

func TestSweepResumeRebuildsInput(t *testing.T) {
	sf := newSweepFixture(t, 100)
	sf.statuses.docs["prov-sweep"] = ProviderStatus{
		ID: "prov-sweep", Status: "completed", HasDuration: true, DurationSeconds: 100,
	}
	raw, err := json.Marshal(SweepInput{MarginSeconds: DefaultMarginSeconds})
	if err != nil {
		t.Fatalf("encode input: %v", err)
	}
	kind := sf.sweeper.Kind()
	if !kind.Idempotent || kind.MaxAttempts != 3 {
		t.Fatalf("sweep kind %+v, want idempotent with three attempts", kind)
	}
	fn, err := kind.Resume(job.Record{Progress: job.Progress{
		Stage:  "list",
		Detail: json.RawMessage(raw),
	}})
	if err != nil {
		t.Fatalf("resume rebuild: %v", err)
	}
	data, err := fn(t.Context(), func(job.Progress) {})
	if err != nil {
		t.Fatalf("resumed work: %v", err)
	}
	var out []SweepOutcome
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decode resumed result: %v", err)
	}
	if len(out) != 1 || !out[0].Skipped {
		t.Fatalf("resumed outcome %+v, want one skipped session", out)
	}
	if _, err := kind.Resume(job.Record{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("resume without input succeeded, want ErrInvalid (err %v)", err)
	}
}

func TestNewSweeperRefusesBadConfig(t *testing.T) {
	fx := newRecFixture(t)
	good := SweeperConfig{
		DB:         fx.db,
		Source:     sweepSource{},
		Statuses:   sweepStatuses{},
		Ender:      &sweepEnder{},
		Reconciler: fx.rec,
	}
	if _, err := NewSweeper(good); err != nil {
		t.Fatalf("good config refused: %v", err)
	}
	bad := good
	bad.DB = nil
	if _, err := NewSweeper(bad); !errors.Is(err, ErrSweep) {
		t.Fatalf("nil database err %v, want ErrSweep", err)
	}
	bad = good
	bad.Source = nil
	if _, err := NewSweeper(bad); !errors.Is(err, ErrSweep) {
		t.Fatalf("nil source err %v, want ErrSweep", err)
	}
	bad = good
	bad.Statuses = nil
	if _, err := NewSweeper(bad); !errors.Is(err, ErrSweep) {
		t.Fatalf("nil statuses err %v, want ErrSweep", err)
	}
	bad = good
	bad.Ender = nil
	if _, err := NewSweeper(bad); !errors.Is(err, ErrSweep) {
		t.Fatalf("nil ender err %v, want ErrSweep", err)
	}
	bad = good
	bad.Reconciler = nil
	if _, err := NewSweeper(bad); !errors.Is(err, ErrSweep) {
		t.Fatalf("nil reconciler err %v, want ErrSweep", err)
	}
	bad = good
	bad.MarginSeconds = -1
	if _, err := NewSweeper(bad); !errors.Is(err, ErrSweep) {
		t.Fatalf("negative margin err %v, want ErrSweep", err)
	}
}

func TestSweepRefusesBadCandidate(t *testing.T) {
	sf := newSweepFixture(t, 4000)
	bad := sf.candidate
	bad.SessionID = ""
	sf.source.listed = []Candidate{bad}
	if _, err := sf.sweeper.Sweep(t.Context(), SweepInput{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad candidate err %v, want ErrInvalid", err)
	}
}

func TestReconcilerIsSettled(t *testing.T) {
	fx := newRecFixture(t)
	in := fx.mintSession("prov-a")
	settled, err := fx.rec.IsSettled(t.Context(), in.SessionID)
	if err != nil {
		t.Fatalf("settled check: %v", err)
	}
	if settled {
		t.Fatalf("fresh session reads settled")
	}
	if _, err := fx.rec.Reconcile(t.Context(), in); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	settled, err = fx.rec.IsSettled(t.Context(), in.SessionID)
	if err != nil {
		t.Fatalf("settled check: %v", err)
	}
	if !settled {
		t.Fatalf("reconciled session reads unsettled")
	}
	if _, err := fx.rec.IsSettled(t.Context(), ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty id err %v, want ErrInvalid", err)
	}
}
