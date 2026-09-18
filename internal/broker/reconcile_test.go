package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nrynss/keel/cost"
	costsqlitestore "github.com/nrynss/keel/cost/sqlitestore"
	"github.com/nrynss/keel/id"
	"github.com/nrynss/keel/job"
	jobsqlitestore "github.com/nrynss/keel/job/sqlitestore"
	"github.com/nrynss/keel/lease"
	leasesqlitestore "github.com/nrynss/keel/lease/sqlitestore"
	"github.com/nrynss/keel/mediastore"
	mediasqlitestore "github.com/nrynss/keel/mediastore/sqlitestore"
	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/keel/stream"
	"github.com/nrynss/reprise/internal/store"
)

const (
	recCapSeconds = 1800
	recOwner      = "owner-reconcile-probe"
	recRate       = cost.Price(1250000)
)

// Recorded provider bodies. The suite never dials the provider. Each body
// pins the one flat shape the decoder reads.
const (
	recDocA     = `{"id":"prov-a","duration_seconds":372,"recording_url":"https://artifacts.example/rec-a.ogg","timeline_url":"https://artifacts.example/tl-a.json"}`
	recDocB     = `{"id":"prov-b","duration_seconds":1800}`
	recDocC     = `{"id":"prov-c","duration_seconds":1920,"recording_url":"https://artifacts.example/rec-c.ogg","timeline_url":"https://artifacts.example/tl-c.json"}`
	recDocShort = `{"id":"prov-short","duration_seconds":10,"recording_url":"https://artifacts.example/rec-short.ogg","timeline_url":"https://artifacts.example/tl-short.json"}`
)

var recAudioA = bytes.Repeat([]byte{0x4f, 0x67, 0x67, 0x53, 0x01, 0x02, 0x03, 0x04}, 512)
var recAudioC = bytes.Repeat([]byte{0x4f, 0x67, 0x67, 0x53, 0x05, 0x06, 0x07, 0x08}, 512)
var recAudioShort = bytes.Repeat([]byte{0x4f, 0x67, 0x67, 0x53, 0x09, 0x0a, 0x0b, 0x0c}, 512)

// recReader replays recorded bodies through the real decoder, so the decode
// stays pinned while no test touches the network.
type recReader struct {
	docs map[string]string
}

func (r recReader) ReadSession(_ context.Context, providerSessionID string) (ProviderSession, error) {
	doc, ok := r.docs[providerSessionID]
	if !ok {
		return ProviderSession{}, fmt.Errorf("recReader: unknown session %s: %w", providerSessionID, ErrRead)
	}
	return ParseProviderSession([]byte(doc))
}

// recFetcher serves artifact bytes from memory.
type recFetcher struct {
	blobs map[string][]byte
}

func (r recFetcher) Fetch(_ context.Context, artifactURL string) (io.ReadCloser, error) {
	raw, ok := r.blobs[artifactURL]
	if !ok {
		return nil, fmt.Errorf("recFetcher: unknown artifact: %w", ErrRead)
	}
	return io.NopCloser(bytes.NewReader(raw)), nil
}

// recDiary writes settled durations on the real sessions table.
type recDiary struct {
	db *sqlite.DB
}

func (d recDiary) SetConnectedSeconds(ctx context.Context, sessionID string, connectedSeconds int) error {
	done, err := d.db.Writer().ExecContext(ctx,
		`UPDATE sessions SET connected_seconds = ? WHERE id = ?`, connectedSeconds, sessionID)
	if err != nil {
		return fmt.Errorf("recDiary: set duration: %w", err)
	}
	won, err := done.RowsAffected()
	if err != nil {
		return fmt.Errorf("recDiary: set duration: %w", err)
	}
	if won != 1 {
		return fmt.Errorf("recDiary: set duration: session %s is unknown", sessionID)
	}
	return nil
}

// recAlerter keeps every alert the reconciler raises.
type recAlerter struct {
	alerts []Alert
}

func (a *recAlerter) Report(_ context.Context, alert Alert) error {
	a.alerts = append(a.alerts, alert)
	return nil
}

func (a *recAlerter) ofKind(kind AlertKind) []Alert {
	var out []Alert
	for _, alert := range a.alerts {
		if alert.Kind == kind {
			out = append(out, alert)
		}
	}
	return out
}

type recFixture struct {
	t        *testing.T
	db       *sqlite.DB
	rec      *Reconciler
	budgets  *costsqlitestore.KeyedBudget
	costs    *costsqlitestore.Store
	leases   *lease.Manager
	media    *mediastore.Store
	mediaDir string
	diary    recDiary
	alerter  *recAlerter
	estimate cost.Price
}

func newRecFixture(t *testing.T) *recFixture {
	t.Helper()
	return newRecFixtureClock(t, nil, time.Duration(recCapSeconds)*time.Second)
}

// newRecFixtureClock builds the fixture around the given lease clock and
// cap. A nil clock means the real clock. A test moves a fake clock past a
// short cap, so a lease expires with no sleep.
func newRecFixtureClock(t *testing.T, nowFn func() time.Time, leaseCap time.Duration) *recFixture {
	quiet := slog.New(slog.DiscardHandler)
	db, err := sqlite.Open(t.Context(), sqlite.Config{Path: t.TempDir() + "/reconcile.sqlite", Logger: quiet})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := store.Open(t.Context(), db); err != nil {
		t.Fatalf("open diary schema: %v", err)
	}
	estimate := recRate * cost.Price(recCapSeconds)
	global := estimate * 10
	costs, err := costsqlitestore.Open(t.Context(), costsqlitestore.Config{DB: db, Limit: global})
	if err != nil {
		t.Fatalf("open cost store: %v", err)
	}
	budgets := costsqlitestore.NewKeyedBudget(costs)
	if err := budgets.SetLimit(t.Context(), recOwner, estimate*10); err != nil {
		t.Fatalf("set owner ceiling: %v", err)
	}
	leaseStore, err := leasesqlitestore.Open(t.Context(), leasesqlitestore.Config{DB: db})
	if err != nil {
		t.Fatalf("open lease store: %v", err)
	}
	quota, err := lease.NewQuota(10)
	if err != nil {
		t.Fatalf("new quota: %v", err)
	}
	manager, err := lease.New(lease.Config{
		Quota: quota,
		Meter: passMeter{},
		Store: leaseStore,
		Cap:   leaseCap,
		Kind:  "session",
		Now:   nowFn,
	})
	if err != nil {
		t.Fatalf("new lease manager: %v", err)
	}
	mediaDir := t.TempDir()
	mediaIndex, err := mediasqlitestore.Open(t.Context(), mediasqlitestore.Config{DB: db})
	if err != nil {
		t.Fatalf("open media index: %v", err)
	}
	media, err := mediastore.Open(t.Context(), mediastore.Config{
		Dir:          mediaDir,
		Index:        mediaIndex,
		ContentTypes: []string{"audio/ogg"},
	})
	if err != nil {
		t.Fatalf("open media store: %v", err)
	}
	now := time.Now().UnixMilli()
	if _, err := db.Writer().ExecContext(t.Context(),
		`INSERT INTO users (id, kind, created_at, last_seen_at) VALUES (?, 'guest', ?, ?)`,
		recOwner, now, now); err != nil {
		t.Fatalf("insert owner: %v", err)
	}
	alerter := &recAlerter{}
	rec, err := NewReconciler(ReconcilerConfig{
		DB: db,
		Sessions: recReader{docs: map[string]string{
			"prov-a":     recDocA,
			"prov-b":     recDocB,
			"prov-c":     recDocC,
			"prov-short": recDocShort,
		}},
		Artifacts: recFetcher{blobs: map[string][]byte{
			"https://artifacts.example/rec-a.ogg":     recAudioA,
			"https://artifacts.example/rec-c.ogg":     recAudioC,
			"https://artifacts.example/rec-short.ogg": recAudioShort,
		}},
		Budgets:              budgets,
		Leases:               manager,
		Diary:                recDiary{db: db},
		Media:                media,
		Alerter:              alerter,
		MarginSeconds:        DefaultMarginSeconds,
		RecordingContentType: DefaultRecordingContentType,
		FetchMaxBytes:        DefaultFetchMaxBytes,
	})
	if err != nil {
		t.Fatalf("new reconciler: %v", err)
	}
	return &recFixture{
		t: t, db: db, rec: rec, budgets: budgets, costs: costs,
		leases: manager, media: media, mediaDir: mediaDir,
		diary: recDiary{db: db}, alerter: alerter, estimate: estimate,
	}
}

// mintSession copies the mint time holds: one diary session, one budget
// hold, one lease. It returns the job input the wiring would carry.
func (f *recFixture) mintSession(providerSessionID string) Input {
	f.t.Helper()
	diary, err := NewSQLiteDiary(f.db)
	if err != nil {
		f.t.Fatalf("open diary: %v", err)
	}
	_, sessionID, err := diary.CreateEpisodeAndSession(f.t.Context(), recOwner, recCapSeconds)
	if err != nil {
		f.t.Fatalf("create session row: %v", err)
	}
	if _, err := f.db.Writer().ExecContext(f.t.Context(),
		`UPDATE sessions SET provider_session_id = ? WHERE id = ?`, providerSessionID, sessionID); err != nil {
		f.t.Fatalf("link provider session: %v", err)
	}
	var episodeID string
	if err := f.db.Reader().QueryRowContext(f.t.Context(),
		`SELECT episode_id FROM sessions WHERE id = ?`, sessionID).Scan(&episodeID); err != nil {
		f.t.Fatalf("read episode: %v", err)
	}
	reservation, err := f.budgets.Reserve(f.t.Context(), recOwner, f.estimate)
	if err != nil {
		f.t.Fatalf("reserve: %v", err)
	}
	opened, err := f.leases.Open(f.t.Context(), recOwner, f.estimate, "")
	if err != nil {
		f.t.Fatalf("open lease: %v", err)
	}
	return Input{
		SessionID:         sessionID,
		OwnerID:           recOwner,
		EpisodeID:         episodeID,
		ProviderSessionID: providerSessionID,
		LeaseID:           opened.ID,
		Reservation:       reservation,
		TokenCapSeconds:   recCapSeconds,
	}
}

func recReserved(t *testing.T, costs *costsqlitestore.Store) cost.Price {
	t.Helper()
	held, err := costs.Reserved(t.Context())
	if err != nil {
		t.Fatalf("read held: %v", err)
	}
	return held
}

func recSpent(t *testing.T, costs *costsqlitestore.Store) cost.Price {
	t.Helper()
	spent, err := costs.Spent(t.Context())
	if err != nil {
		t.Fatalf("read spent: %v", err)
	}
	return spent
}

func recConnected(t *testing.T, db *sqlite.DB, sessionID string) int {
	t.Helper()
	var seconds int
	if err := db.Reader().QueryRowContext(t.Context(),
		`SELECT connected_seconds FROM sessions WHERE id = ?`, sessionID).Scan(&seconds); err != nil {
		t.Fatalf("read connected seconds: %v", err)
	}
	return seconds
}

func TestReconcileParsesRecordedBodies(t *testing.T) {
	read, err := ParseProviderSession([]byte(recDocA))
	if err != nil {
		t.Fatalf("parse recorded body: %v", err)
	}
	if read.DurationSeconds != 372 {
		t.Fatalf("duration %d, want 372", read.DurationSeconds)
	}
	if read.RecordingURL != "https://artifacts.example/rec-a.ogg" {
		t.Fatalf("recording URL %q is wrong", read.RecordingURL)
	}
	if read.TimelineURL != "https://artifacts.example/tl-a.json" {
		t.Fatalf("timeline URL %q is wrong", read.TimelineURL)
	}
	read, err = ParseProviderSession([]byte(recDocB))
	if err != nil {
		t.Fatalf("parse minimal body: %v", err)
	}
	if read.DurationSeconds != 1800 || read.RecordingURL != "" || read.TimelineURL != "" {
		t.Fatalf("minimal body decoded wrong: %+v", read)
	}
	for i, doc := range []string{
		`{"id":"prov-x"}`,
		`{"id":"prov-x","duration_seconds":-3}`,
		`{"id":`,
		``,
	} {
		if _, err := ParseProviderSession([]byte(doc)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("body %d parsed, want ErrInvalid (err %v)", i, err)
		}
	}
}

func TestReconcileSettlesEveryHold(t *testing.T) {
	fx := newRecFixture(t)
	inA := fx.mintSession("prov-a")
	inB := fx.mintSession("prov-b")
	inC := fx.mintSession("prov-c")
	if held := recReserved(t, fx.costs); held != fx.estimate*3 {
		t.Fatalf("held %d, want three estimates", held)
	}

	resA, err := fx.rec.Reconcile(t.Context(), inA)
	if err != nil {
		t.Fatalf("reconcile A: %v", err)
	}
	resB, err := fx.rec.Reconcile(t.Context(), inB)
	if err != nil {
		t.Fatalf("reconcile B: %v", err)
	}
	resC, err := fx.rec.Reconcile(t.Context(), inC)
	if err != nil {
		t.Fatalf("reconcile C: %v", err)
	}

	wantSpent := recRate*372 + recRate*1800 + recRate*1920
	if got := recSpent(t, fx.costs); got != wantSpent {
		t.Fatalf("spent %d, want %d", got, wantSpent)
	}
	if held := recReserved(t, fx.costs); held != 0 {
		t.Fatalf("held %d, want none held", held)
	}
	if resA.ConnectedSeconds != 372 || resB.ConnectedSeconds != 1800 || resC.ConnectedSeconds != 1920 {
		t.Fatalf("durations %d/%d/%d are wrong", resA.ConnectedSeconds, resB.ConnectedSeconds, resC.ConnectedSeconds)
	}
	if resA.Cost != recRate*372 || resB.Cost != recRate*1800 || resC.Cost != recRate*1920 {
		t.Fatalf("costs %d/%d/%d are wrong", resA.Cost, resB.Cost, resC.Cost)
	}
	if recConnected(t, fx.db, inA.SessionID) != 372 {
		t.Fatalf("session A row missed its duration")
	}
	if recConnected(t, fx.db, inB.SessionID) != 1800 {
		t.Fatalf("session B row missed its duration")
	}
	for _, in := range []Input{inA, inB, inC} {
		found, err := fx.leases.Inspect(t.Context(), in.LeaseID)
		if err != nil {
			t.Fatalf("inspect lease: %v", err)
		}
		if !found.Reconciled {
			t.Fatalf("lease %s is not reconciled", in.LeaseID)
		}
	}
	if resA.OverCap || resB.OverCap {
		t.Fatalf("session within cap flagged over cap: %+v %+v", resA, resB)
	}
	if !resC.OverCap {
		t.Fatalf("session past cap plus margin missed its flag: %+v", resC)
	}
	over := fx.alerter.ofKind(AlertOverCap)
	if len(over) != 1 || over[0].SessionID != inC.SessionID {
		t.Fatalf("over cap alerts %v, want exactly the C session", fx.alerter.alerts)
	}
	if resA.RecordingMediaID == "" {
		t.Fatalf("session A stored no recording")
	}
	raw, err := os.ReadFile(filepath.Join(fx.mediaDir, resA.RecordingMediaID))
	if err != nil {
		t.Fatalf("read stored recording: %v", err)
	}
	if !bytes.Equal(raw, recAudioA) {
		t.Fatalf("stored recording holds %d bytes, want the artifact bytes", len(raw))
	}
	if resB.RecordingMediaID != "" {
		t.Fatalf("session B without a recording URL stored %q", resB.RecordingMediaID)
	}
	if resC.TimelineURL != "https://artifacts.example/tl-c.json" {
		t.Fatalf("session C timeline %q is wrong", resC.TimelineURL)
	}
}

func TestReconcileSecondRunSettlesOnce(t *testing.T) {
	fx := newRecFixture(t)
	in := fx.mintSession("prov-a")
	first, err := fx.rec.Reconcile(t.Context(), in)
	if err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	spent := recSpent(t, fx.costs)
	second, err := fx.rec.Reconcile(t.Context(), in)
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if got := recSpent(t, fx.costs); got != spent {
		t.Fatalf("spent moved %d to %d on retry", spent, got)
	}
	if held := recReserved(t, fx.costs); held != 0 {
		t.Fatalf("held %d after retry, want none held", held)
	}
	if second != first {
		t.Fatalf("retry result %+v differs from %+v", second, first)
	}
}

func TestReconcileExpiredLeaseCompletesTail(t *testing.T) {
	start := time.Now()
	current := start
	fx := newRecFixtureClock(t, func() time.Time { return current }, 150*time.Millisecond)
	in := fx.mintSession("prov-short")
	spentBefore := recSpent(t, fx.costs)
	current = start.Add(time.Second)
	res, err := fx.rec.Reconcile(t.Context(), in)
	if err != nil {
		t.Fatalf("reconcile past the lease cap: %v", err)
	}
	if res.NeedsReview {
		t.Fatalf("expired lease asked for review: %+v", res)
	}
	if res.ConnectedSeconds != 10 || res.Cost != recRate*10 {
		t.Fatalf("expired tail settled %+v, want 10 seconds at %d", res, recRate*10)
	}
	if recConnected(t, fx.db, in.SessionID) != 10 {
		t.Fatalf("session row missed its duration")
	}
	if res.RecordingMediaID == "" {
		t.Fatalf("expired tail stored no recording")
	}
	raw, err := os.ReadFile(filepath.Join(fx.mediaDir, res.RecordingMediaID))
	if err != nil {
		t.Fatalf("read stored recording: %v", err)
	}
	if !bytes.Equal(raw, recAudioShort) {
		t.Fatalf("stored recording holds %d bytes, want the artifact bytes", len(raw))
	}
	if got := recSpent(t, fx.costs); got != spentBefore+recRate*10 {
		t.Fatalf("spent %d, want exactly one settle past %d", got, spentBefore)
	}
	if len(fx.alerter.alerts) != 0 {
		t.Fatalf("in cap run raised alerts %v, want none", fx.alerter.alerts)
	}
	found, err := fx.leases.Inspect(t.Context(), in.LeaseID)
	if err != nil {
		t.Fatalf("inspect lease: %v", err)
	}
	if found.State != lease.StateExpired {
		t.Fatalf("lease reads %s, want expired", found.State)
	}
	second, err := fx.rec.Reconcile(t.Context(), in)
	if err != nil {
		t.Fatalf("retry reconcile: %v", err)
	}
	if second.NeedsReview {
		t.Fatalf("retry asked for review: %+v", second)
	}
	if second != res {
		t.Fatalf("retry result %+v differs from %+v", second, res)
	}
	if got := recSpent(t, fx.costs); got != spentBefore+recRate*10 {
		t.Fatalf("spent moved to %d on retry, want no second settle", got)
	}
}

func TestReconcileOverCapAlertsOnce(t *testing.T) {
	fx := newRecFixture(t)
	in := fx.mintSession("prov-c")
	first, err := fx.rec.Reconcile(t.Context(), in)
	if err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if !first.OverCap || first.NeedsReview {
		t.Fatalf("first result %+v missed its over cap flag", first)
	}
	second, err := fx.rec.Reconcile(t.Context(), in)
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if second.NeedsReview {
		t.Fatalf("retry asked for review: %+v", second)
	}
	over := fx.alerter.ofKind(AlertOverCap)
	if len(over) != 1 || over[0].SessionID != in.SessionID {
		t.Fatalf("over cap alerts %v, want exactly one for this session", fx.alerter.alerts)
	}
	if got := recSpent(t, fx.costs); got != recRate*1920 {
		t.Fatalf("spent %d, want exactly one settle", got)
	}
}

func TestReconcileAlertColumnMigrates(t *testing.T) {
	fx := newRecFixture(t)
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`ALTER TABLE reconcile_state DROP COLUMN over_cap_alerted`); err != nil {
		t.Fatalf("drop alert column: %v", err)
	}
	if _, err := NewReconciler(ReconcilerConfig{
		DB:        fx.db,
		Sessions:  recReader{},
		Artifacts: recFetcher{},
		Budgets:   fx.budgets,
		Leases:    fx.leases,
		Diary:     fx.diary,
		Media:     fx.media,
	}); err != nil {
		t.Fatalf("reopen reconciler: %v", err)
	}
	rows, err := fx.db.Reader().QueryContext(t.Context(), `PRAGMA table_info(reconcile_state)`)
	if err != nil {
		t.Fatalf("read columns: %v", err)
	}
	defer rows.Close()
	restored := false
	for rows.Next() {
		var cid, notNull, pk int
		var name, ctype string
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		if name == "over_cap_alerted" {
			restored = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read columns: %v", err)
	}
	if !restored {
		t.Fatalf("alert column is missing after reopen")
	}
}

func TestReconcileAmbiguousResumeSettlesNothing(t *testing.T) {
	fx := newRecFixture(t)
	in := fx.mintSession("prov-a")
	rec := fx.rec
	if err := rec.ensureRow(t.Context(), in.SessionID); err != nil {
		t.Fatalf("ensure row: %v", err)
	}
	won, err := rec.claim(t.Context(), in.SessionID)
	if err != nil || !won {
		t.Fatalf("pre-claim failed: won %v err %v", won, err)
	}
	res, err := rec.Reconcile(t.Context(), in)
	if err != nil {
		t.Fatalf("ambiguous resume: %v", err)
	}
	if !res.NeedsReview {
		t.Fatalf("ambiguous resume settled or skipped silently: %+v", res)
	}
	if got := recSpent(t, fx.costs); got != 0 {
		t.Fatalf("spent %d on an ambiguous resume, want zero", got)
	}
	if held := recReserved(t, fx.costs); held != fx.estimate {
		t.Fatalf("held %d, want the untouched estimate", held)
	}
	reviews := fx.alerter.ofKind(AlertNeedsReview)
	if len(reviews) != 1 || reviews[0].SessionID != in.SessionID {
		t.Fatalf("review alerts %v, want exactly this session", fx.alerter.alerts)
	}
}

func TestReconcileClosedLeaseResumeFinishes(t *testing.T) {
	fx := newRecFixture(t)
	in := fx.mintSession("prov-a")
	price := recRate * 372
	if err := fx.rec.budgets.Settle(t.Context(), in.OwnerID, in.Reservation, price); err != nil {
		t.Fatalf("settle hold: %v", err)
	}
	if _, err := fx.leases.Close(t.Context(), in.LeaseID, price); err != nil {
		t.Fatalf("close lease: %v", err)
	}
	rec := fx.rec
	if err := rec.ensureRow(t.Context(), in.SessionID); err != nil {
		t.Fatalf("ensure row: %v", err)
	}
	if _, err := rec.claim(t.Context(), in.SessionID); err != nil {
		t.Fatalf("claim row: %v", err)
	}
	res, err := rec.Reconcile(t.Context(), in)
	if err != nil {
		t.Fatalf("resume after close: %v", err)
	}
	if res.NeedsReview {
		t.Fatalf("closed lease resume asked for review: %+v", res)
	}
	if res.ConnectedSeconds != 372 || res.Cost != price {
		t.Fatalf("resume settled %+v, want 372 seconds at %d", res, price)
	}
	if got := recSpent(t, fx.costs); got != price {
		t.Fatalf("spent %d, want exactly one settle at %d", got, price)
	}
	if held := recReserved(t, fx.costs); held != 0 {
		t.Fatalf("held %d, want none held", held)
	}
	found, err := fx.leases.Inspect(t.Context(), in.LeaseID)
	if err != nil {
		t.Fatalf("inspect lease: %v", err)
	}
	if !found.Reconciled || found.Settled != price {
		t.Fatalf("lease %+v missed its reconcile", found)
	}
}

func TestReconcileResumeRebuildsInput(t *testing.T) {
	fx := newRecFixture(t)
	in := fx.mintSession("prov-a")
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("encode input: %v", err)
	}
	fn, err := fx.rec.Kind().Resume(job.Record{Progress: job.Progress{
		Stage:  "read",
		Detail: json.RawMessage(raw),
	}})
	if err != nil {
		t.Fatalf("resume rebuild: %v", err)
	}
	data, err := fn(t.Context(), func(job.Progress) {})
	if err != nil {
		t.Fatalf("resumed work: %v", err)
	}
	var res Result
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatalf("decode resumed result: %v", err)
	}
	if res.ConnectedSeconds != 372 || res.NeedsReview {
		t.Fatalf("resumed result %+v is wrong", res)
	}
	if got := recSpent(t, fx.costs); got != recRate*372 {
		t.Fatalf("spent %d, want exactly one settle", got)
	}
	if _, err := fx.rec.Kind().Resume(job.Record{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("resume without input succeeded, want ErrInvalid (err %v)", err)
	}
}

func TestReconcileRestartResumesAndSettlesOnce(t *testing.T) {
	fx := newRecFixture(t)
	in := fx.mintSession("prov-a")
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("encode input: %v", err)
	}
	jobStore, err := jobsqlitestore.Open(t.Context(), jobsqlitestore.Config{DB: fx.db})
	if err != nil {
		t.Fatalf("open job store: %v", err)
	}
	jobID, err := id.New()
	if err != nil {
		t.Fatalf("mint job id: %v", err)
	}
	stalled := job.Record{
		ID:        jobID,
		Kind:      KindName,
		Status:    job.StatusRunning,
		Attempt:   1,
		RootID:    jobID,
		Progress:  job.Progress{Stage: "read", Detail: json.RawMessage(raw)},
		UpdatedAt: time.Now(),
	}
	if err := jobStore.Create(t.Context(), stalled); err != nil {
		t.Fatalf("stage stalled record: %v", err)
	}
	runner, err := job.Open(t.Context(), job.Config{
		Broker: stream.New(stream.Config{}),
		Store:  jobStore,
		Kinds:  map[string]job.Kind{KindName: fx.rec.Kind()},
	})
	if err != nil {
		t.Fatalf("reopen runner: %v", err)
	}
	_ = runner
	deadline := time.Now().Add(15 * time.Second)
	for {
		unfinished, err := jobStore.Unfinished(t.Context())
		if err != nil {
			t.Fatalf("list unfinished: %v", err)
		}
		if len(unfinished) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("resumed job never finished")
		}
		time.Sleep(20 * time.Millisecond)
	}
	attempts, err := jobStore.Attempts(t.Context(), jobID)
	if err != nil {
		t.Fatalf("list attempts: %v", err)
	}
	var done *job.Record
	for i := range attempts {
		if attempts[i].Status == job.StatusDone {
			done = &attempts[i]
		}
	}
	if done == nil {
		t.Fatalf("no done attempt in %+v", attempts)
	}
	var res Result
	if err := json.Unmarshal(done.Data, &res); err != nil {
		t.Fatalf("decode done result: %v", err)
	}
	if res.ConnectedSeconds != 372 || res.NeedsReview {
		t.Fatalf("restart result %+v is wrong", res)
	}
	if got := recSpent(t, fx.costs); got != recRate*372 {
		t.Fatalf("spent %d after restart, want exactly one settle", got)
	}
	if held := recReserved(t, fx.costs); held != 0 {
		t.Fatalf("held %d after restart, want none held", held)
	}
}

func TestReconcileRecordingStaysPrivate(t *testing.T) {
	fx := newRecFixture(t)
	in := fx.mintSession("prov-a")
	res, err := fx.rec.Reconcile(t.Context(), in)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.RecordingMediaID == "" {
		t.Fatalf("no recording was stored")
	}
	req := httptest.NewRequest(http.MethodGet, "/media/"+res.RecordingMediaID, nil)
	req.SetPathValue("id", res.RecordingMediaID)
	rec := httptest.NewRecorder()
	fx.media.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("private recording answered %d, want 404 without a grant", rec.Code)
	}
}

func TestHTTPArtifactFetcher(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(recAudioA)
	}))
	defer server.Close()
	fetcher := NewHTTPArtifactFetcher(server.Client())
	body, err := fetcher.Fetch(t.Context(), server.URL+"/recording")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	raw, err := io.ReadAll(body)
	body.Close()
	if err != nil {
		t.Fatalf("read fetched bytes: %v", err)
	}
	if !bytes.Equal(raw, recAudioA) {
		t.Fatalf("fetched %d bytes, want the artifact bytes", len(raw))
	}
	if _, err := fetcher.Fetch(t.Context(), server.URL+"/missing"); !errors.Is(err, ErrRead) {
		t.Fatalf("missing artifact err %v, want ErrRead", err)
	}
	if _, err := fetcher.Fetch(t.Context(), ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty URL err %v, want ErrInvalid", err)
	}
}

func TestPriceForSecondsRefusesOverflow(t *testing.T) {
	huge := int(math.MaxInt64/recRate) + 1
	if _, err := priceForSeconds(huge); !errors.Is(err, ErrInvalid) {
		t.Fatalf("overflow price err %v, want ErrInvalid", err)
	}
	if _, err := priceForSeconds(-1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative price err %v, want ErrInvalid", err)
	}
	if got, err := priceForSeconds(372); err != nil || got != recRate*372 {
		t.Fatalf("price %d err %v, want %d", got, err, recRate*372)
	}
}

func TestNewReconcilerRefusesBadConfig(t *testing.T) {
	fx := newRecFixture(t)
	good := ReconcilerConfig{
		DB:        fx.db,
		Sessions:  recReader{},
		Artifacts: recFetcher{},
		Budgets:   fx.budgets,
		Leases:    fx.leases,
		Diary:     fx.diary,
		Media:     fx.media,
	}
	bad := []ReconcilerConfig{
		{},
		func() ReconcilerConfig { c := good; c.DB = nil; return c }(),
		func() ReconcilerConfig { c := good; c.Sessions = nil; return c }(),
		func() ReconcilerConfig { c := good; c.Artifacts = nil; return c }(),
		func() ReconcilerConfig { c := good; c.Budgets = nil; return c }(),
		func() ReconcilerConfig { c := good; c.Leases = nil; return c }(),
		func() ReconcilerConfig { c := good; c.Diary = nil; return c }(),
		func() ReconcilerConfig { c := good; c.Media = nil; return c }(),
		func() ReconcilerConfig { c := good; c.MarginSeconds = -1; return c }(),
	}
	for i, cfg := range bad {
		if _, err := NewReconciler(cfg); err == nil {
			t.Fatalf("config %d built, want an error", i)
		}
	}
	if _, err := NewReconciler(good); err != nil {
		t.Fatalf("good config refused: %v", err)
	}
	if kind := fx.rec.Kind(); !kind.Idempotent || kind.MaxAttempts != 3 || kind.Resume == nil {
		t.Fatalf("job kind %+v misses idempotent, 3 attempts, or resume", kind)
	}
}
