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
	"sync"
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

// Recorded timeline bodies. The provider names a timeline beside every
// recording, and the reconciler stores both, because both URLs expire.
var recTimelineA = []byte(`{"session":"prov-a","turns":[{"speaker":"host","start_ms":0}]}`)
var recTimelineC = []byte(`{"session":"prov-c","turns":[]}`)
var recTimelineShort = []byte(`{"session":"prov-short","turns":[]}`)

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
		ContentTypes: []string{"audio/ogg", "application/json"},
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
			"https://artifacts.example/tl-a.json":     recTimelineA,
			"https://artifacts.example/rec-c.ogg":     recAudioC,
			"https://artifacts.example/tl-c.json":     recTimelineC,
			"https://artifacts.example/rec-short.ogg": recAudioShort,
			"https://artifacts.example/tl-short.json": recTimelineShort,
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
	if resA.TimelineMediaID == "" {
		t.Fatalf("session A stored no timeline")
	}
	timeline, err := os.ReadFile(filepath.Join(fx.mediaDir, resA.TimelineMediaID))
	if err != nil {
		t.Fatalf("read stored timeline: %v", err)
	}
	if !bytes.Equal(timeline, recTimelineA) {
		t.Fatalf("stored timeline holds %d bytes, want the artifact bytes", len(timeline))
	}
	if resB.RecordingMediaID != "" {
		t.Fatalf("session B without a recording URL stored %q", resB.RecordingMediaID)
	}
	if resB.TimelineMediaID != "" {
		t.Fatalf("session B without a timeline URL stored %q", resB.TimelineMediaID)
	}
	if resC.TimelineMediaID == "" {
		t.Fatalf("session C stored no timeline")
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

// countingFetcher serves artifact bytes and counts every fetch per URL.
// The counts pin that a repeated end fetches nothing new.
type countingFetcher struct {
	mu      sync.Mutex
	blobs   map[string][]byte
	fetches map[string]int
}

func (f *countingFetcher) Fetch(_ context.Context, artifactURL string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, ok := f.blobs[artifactURL]
	if !ok {
		return nil, fmt.Errorf("countingFetcher: unknown artifact: %w", ErrRead)
	}
	if f.fetches == nil {
		f.fetches = map[string]int{}
	}
	f.fetches[artifactURL]++
	return io.NopCloser(bytes.NewReader(raw)), nil
}

func (f *countingFetcher) count(artifactURL string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fetches[artifactURL]
}

// recMediaTypes counts the stored blobs for one episode by content type.
// The episode group holds the recording and the timeline beside it.
func recMediaTypes(t *testing.T, db *sqlite.DB, episodeID string) map[string]int {
	t.Helper()
	rows, err := db.Reader().QueryContext(t.Context(),
		`SELECT content_type, COUNT(*) FROM media WHERE media_group = ? GROUP BY content_type`, episodeID)
	if err != nil {
		t.Fatalf("list episode media: %v", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var contentType string
		var held int
		if err := rows.Scan(&contentType, &held); err != nil {
			t.Fatalf("scan episode media: %v", err)
		}
		out[contentType] = held
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list episode media: %v", err)
	}
	return out
}

// TestReconcileRepeatEndStoresNothingNew posts the end twice for one
// session. The first reconcile settles and stores one recording and one
// timeline. The second returns the stored ids without fetching,
// spending, or storing again.
func TestReconcileRepeatEndStoresNothingNew(t *testing.T) {
	fx := newRecFixture(t)
	in := fx.mintSession("prov-a")
	fetcher := &countingFetcher{blobs: map[string][]byte{
		"https://artifacts.example/rec-a.ogg": recAudioA,
		"https://artifacts.example/tl-a.json": recTimelineA,
	}}
	fx.rec.artifacts = fetcher

	first, err := fx.rec.Reconcile(t.Context(), in)
	if err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if first.RecordingMediaID == "" || first.TimelineMediaID == "" {
		t.Fatalf("first result %+v stored no recording and timeline pair", first)
	}
	if first.RecordingMediaID == first.TimelineMediaID {
		t.Fatalf("recording and timeline share media %q, want two blobs", first.RecordingMediaID)
	}
	spent := recSpent(t, fx.costs)

	second, err := fx.rec.Reconcile(t.Context(), in)
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if second != first {
		t.Fatalf("second result %+v differs from %+v", second, first)
	}
	if got := fetcher.count("https://artifacts.example/rec-a.ogg"); got != 1 {
		t.Fatalf("recording fetched %d times, want exactly once", got)
	}
	if got := fetcher.count("https://artifacts.example/tl-a.json"); got != 1 {
		t.Fatalf("timeline fetched %d times, want exactly once", got)
	}
	if got := recSpent(t, fx.costs); got != spent {
		t.Fatalf("spent moved %d to %d on a repeat end", spent, got)
	}
	if held := recReserved(t, fx.costs); held != 0 {
		t.Fatalf("held %d after a repeat end, want none held", held)
	}
	held := recMediaTypes(t, fx.db, in.EpisodeID)
	if held["audio/ogg"] != 1 || held["application/json"] != 1 || len(held) != 2 {
		t.Fatalf("episode media %v, want one recording and one timeline", held)
	}
}

// TestReconcileConcurrentEndsStoreOnce runs two reconciles for one
// session at once, the way two end posts start two jobs. One pass
// settles and stores, the other waits and then reuses the stored rows,
// so the media table holds one recording and one timeline.
func TestReconcileConcurrentEndsStoreOnce(t *testing.T) {
	fx := newRecFixture(t)
	in := fx.mintSession("prov-a")
	fetcher := &countingFetcher{blobs: map[string][]byte{
		"https://artifacts.example/rec-a.ogg": recAudioA,
		"https://artifacts.example/tl-a.json": recTimelineA,
	}}
	fx.rec.artifacts = fetcher

	const ends = 2
	results := make([]Result, ends)
	errs := make([]error, ends)
	ready := make(chan struct{})
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-ready
			res, err := fx.rec.Reconcile(t.Context(), in)
			results[i], errs[i] = res, err
		}()
	}
	close(ready)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("end %d: %v", i, err)
		}
	}
	if results[0] != results[1] {
		t.Fatalf("concurrent results %+v and %+v differ", results[0], results[1])
	}
	if results[0].RecordingMediaID == "" || results[0].TimelineMediaID == "" {
		t.Fatalf("concurrent result %+v stored no recording and timeline pair", results[0])
	}
	if got := fetcher.count("https://artifacts.example/rec-a.ogg"); got != 1 {
		t.Fatalf("recording fetched %d times, want exactly once", got)
	}
	if got := fetcher.count("https://artifacts.example/tl-a.json"); got != 1 {
		t.Fatalf("timeline fetched %d times, want exactly once", got)
	}
	if got := recSpent(t, fx.costs); got != recRate*372 {
		t.Fatalf("spent %d, want exactly one settle at %d", got, recRate*372)
	}
	if held := recReserved(t, fx.costs); held != 0 {
		t.Fatalf("held %d after concurrent ends, want none held", held)
	}
	held := recMediaTypes(t, fx.db, in.EpisodeID)
	if held["audio/ogg"] != 1 || held["application/json"] != 1 || len(held) != 2 {
		t.Fatalf("episode media %v, want one recording and one timeline", held)
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

func TestReconcileTimelineColumnMigrates(t *testing.T) {
	fx := newRecFixture(t)
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`ALTER TABLE reconcile_state DROP COLUMN timeline_media_id`); err != nil {
		t.Fatalf("drop timeline column: %v", err)
	}
	rec, err := NewReconciler(ReconcilerConfig{
		DB:       fx.db,
		Sessions: recReader{docs: map[string]string{"prov-a": recDocA}},
		Artifacts: recFetcher{blobs: map[string][]byte{
			"https://artifacts.example/rec-a.ogg": recAudioA,
			"https://artifacts.example/tl-a.json": recTimelineA,
		}},
		Budgets: fx.budgets,
		Leases:  fx.leases,
		Diary:   fx.diary,
		Media:   fx.media,
	})
	if err != nil {
		t.Fatalf("reopen reconciler: %v", err)
	}
	in := fx.mintSession("prov-a")
	res, err := rec.Reconcile(t.Context(), in)
	if err != nil {
		t.Fatalf("reconcile on migrated table: %v", err)
	}
	if res.RecordingMediaID == "" || res.TimelineMediaID == "" {
		t.Fatalf("migrated result %+v stored no recording and timeline pair", res)
	}
	held := recMediaTypes(t, fx.db, in.EpisodeID)
	if held["audio/ogg"] != 1 || held["application/json"] != 1 || len(held) != 2 {
		t.Fatalf("episode media %v, want one recording and one timeline", held)
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

// commitThenFailWriter stores through the real media store, then fails
// the first successful write. The blob is committed and the caller still
// sees an error, which is the gap after a commit and before the next step.
type commitThenFailWriter struct {
	inner  *mediastore.Store
	mu     sync.Mutex
	failed bool
}

func (w *commitThenFailWriter) Persist(ctx context.Context, src io.Reader, put mediastore.Put) (string, error) {
	mediaID, err := w.inner.Persist(ctx, src, put)
	if err != nil {
		return "", err
	}
	if w.failFirst() {
		return "", errors.New("persist committed then the writer failed")
	}
	return mediaID, nil
}

func (w *commitThenFailWriter) PersistWithID(ctx context.Context, blobID string, src io.Reader, put mediastore.Put) error {
	if err := w.inner.PersistWithID(ctx, blobID, src, put); err != nil {
		return err
	}
	if w.failFirst() {
		return errors.New("persist committed then the writer failed")
	}
	return nil
}

func (w *commitThenFailWriter) failFirst() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failed {
		return false
	}
	w.failed = true
	return true
}

// holdFetcher counts artifact reads. It blocks every recording read until
// release is closed, so a second reconciler can run while the first is
// still inside that read.
type holdFetcher struct {
	mu           sync.Mutex
	blobs        map[string][]byte
	fetches      map[string]int
	recordingURL string
	timelineURL  string
	firstIn      chan struct{}
	secondIn     chan struct{}
	timelineIn   chan struct{}
	release      chan struct{}
	held         int
	onceFirst    sync.Once
	onceSecond   sync.Once
	onceTimeline sync.Once
}

func (f *holdFetcher) Fetch(ctx context.Context, artifactURL string) (io.ReadCloser, error) {
	f.mu.Lock()
	if f.fetches == nil {
		f.fetches = map[string]int{}
	}
	f.fetches[artifactURL]++
	raw, ok := f.blobs[artifactURL]
	f.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("holdFetcher: unknown artifact: %w", ErrRead)
	}
	if artifactURL == f.recordingURL {
		f.noteRecording()
		select {
		case <-f.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if artifactURL == f.timelineURL {
		f.onceTimeline.Do(func() { close(f.timelineIn) })
	}
	return io.NopCloser(bytes.NewReader(raw)), nil
}

func (f *holdFetcher) noteRecording() {
	f.mu.Lock()
	f.held++
	n := f.held
	f.mu.Unlock()
	if n == 1 {
		f.onceFirst.Do(func() { close(f.firstIn) })
		return
	}
	f.onceSecond.Do(func() { close(f.secondIn) })
}

func (f *holdFetcher) count(artifactURL string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fetches[artifactURL]
}

func (f *recFixture) openReconciler(media MediaWriter, artifacts ArtifactFetcher) *Reconciler {
	f.t.Helper()
	rec, err := NewReconciler(ReconcilerConfig{
		DB:                   f.db,
		Sessions:             f.rec.sessions,
		Artifacts:            artifacts,
		Budgets:              f.budgets,
		Leases:               f.leases,
		Diary:                f.diary,
		Media:                media,
		Alerter:              f.alerter,
		MarginSeconds:        DefaultMarginSeconds,
		RecordingContentType: DefaultRecordingContentType,
		FetchMaxBytes:        DefaultFetchMaxBytes,
	})
	if err != nil {
		f.t.Fatalf("open reconciler: %v", err)
	}
	return rec
}

func recPrivateTypes(t *testing.T, db *sqlite.DB, episodeID string) map[string]int {
	t.Helper()
	rows, err := db.Reader().QueryContext(t.Context(),
		`SELECT content_type, visibility, owner FROM media WHERE media_group = ?`, episodeID)
	if err != nil {
		t.Fatalf("list episode media: %v", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var contentType, visibility, owner string
		if err := rows.Scan(&contentType, &visibility, &owner); err != nil {
			t.Fatalf("scan episode media: %v", err)
		}
		if visibility != "private" || owner != recOwner {
			t.Fatalf("blob %s visibility %q owner %q, want private %s", contentType, visibility, owner, recOwner)
		}
		out[contentType]++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list episode media: %v", err)
	}
	return out
}

func recClaimState(t *testing.T, db *sqlite.DB, sessionID string) (bool, string, string) {
	t.Helper()
	var settled int
	var recordingID, timelineID string
	if err := db.Reader().QueryRowContext(t.Context(),
		`SELECT settled, recording_media_id, timeline_media_id FROM reconcile_state WHERE session_id = ?`,
		sessionID).Scan(&settled, &recordingID, &timelineID); err != nil {
		t.Fatalf("read claim: %v", err)
	}
	return settled != 0, recordingID, timelineID
}

// TestReconcileCommitThenFailStoresOneRecording commits one recording and
// then fails the writer. The claim is settled and one private recording
// is stored. A second reconciler must not add another recording or
// timeline, and must not fetch the recording again.
func TestReconcileCommitThenFailStoresOneRecording(t *testing.T) {
	fx := newRecFixture(t)
	in := fx.mintSession("prov-a")
	const recordingURL = "https://artifacts.example/rec-a.ogg"
	const timelineURL = "https://artifacts.example/tl-a.json"
	fetcher := &countingFetcher{blobs: map[string][]byte{
		recordingURL: recAudioA,
		timelineURL:  recTimelineA,
	}}
	writer := &commitThenFailWriter{inner: fx.media}
	fx.rec.artifacts = fetcher
	fx.rec.media = writer

	if _, err := fx.rec.Reconcile(t.Context(), in); err == nil {
		t.Fatal("reconcile succeeded, want the writer error after the blob committed")
	}
	settled, _, _ := recClaimState(t, fx.db, in.SessionID)
	if !settled {
		t.Fatal("claim is not settled after the failed persist")
	}
	if held := recPrivateTypes(t, fx.db, in.EpisodeID); held["audio/ogg"] != 1 || len(held) != 1 {
		t.Fatalf("episode media %v, want one private recording", held)
	}
	if got := fetcher.count(recordingURL); got != 1 {
		t.Fatalf("recording fetched %d times before the retry, want one", got)
	}
	spent := recSpent(t, fx.costs)
	if spent != recRate*372 {
		t.Fatalf("spent %d, want one settle of %d", spent, recRate*372)
	}

	next := fx.openReconciler(writer, fetcher)
	if _, err := next.Reconcile(t.Context(), in); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	held := recPrivateTypes(t, fx.db, in.EpisodeID)
	if held["audio/ogg"] != 1 || held["application/json"] != 1 || len(held) != 2 {
		t.Fatalf("episode media %v, want one recording and one timeline", held)
	}
	if got := fetcher.count(recordingURL); got != 1 {
		t.Fatalf("recording fetched %d times, want exactly once", got)
	}
	if got := fetcher.count(timelineURL); got != 1 {
		t.Fatalf("timeline fetched %d times, want exactly once", got)
	}
	if got := recSpent(t, fx.costs); got != spent {
		t.Fatalf("spent moved %d to %d on the second reconciler", spent, got)
	}
}

// TestReconcileTwoValuesStoreOnePair runs two reconcilers on one database
// and one media store. The recording read stays blocked until the other
// call has entered it or has fetched the timeline. The table ends with
// one private recording and one private timeline. Each URL is fetched
// once and spend moves once.
func TestReconcileTwoValuesStoreOnePair(t *testing.T) {
	fx := newRecFixture(t)
	in := fx.mintSession("prov-a")
	const recordingURL = "https://artifacts.example/rec-a.ogg"
	const timelineURL = "https://artifacts.example/tl-a.json"
	fetcher := &holdFetcher{
		blobs: map[string][]byte{
			recordingURL: recAudioA,
			timelineURL:  recTimelineA,
		},
		recordingURL: recordingURL,
		timelineURL:  timelineURL,
		firstIn:      make(chan struct{}),
		secondIn:     make(chan struct{}),
		timelineIn:   make(chan struct{}),
		release:      make(chan struct{}),
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(fetcher.release) }) }
	defer release()

	first := fx.rec
	first.artifacts = fetcher
	second := fx.openReconciler(fx.media, fetcher)
	var errA, errB error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, errA = first.Reconcile(t.Context(), in)
	}()
	<-fetcher.firstIn
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, errB = second.Reconcile(t.Context(), in)
	}()
	select {
	case <-fetcher.secondIn:
	case <-fetcher.timelineIn:
	}
	release()
	wg.Wait()
	if errA != nil {
		t.Fatalf("first reconcile: %v", errA)
	}
	if errB != nil {
		t.Fatalf("second reconcile: %v", errB)
	}
	held := recPrivateTypes(t, fx.db, in.EpisodeID)
	if held["audio/ogg"] != 1 || held["application/json"] != 1 || len(held) != 2 {
		t.Fatalf("episode media %v, want one recording and one timeline", held)
	}
	if got := fetcher.count(recordingURL); got != 1 {
		t.Fatalf("recording fetched %d times, want exactly once", got)
	}
	if got := fetcher.count(timelineURL); got != 1 {
		t.Fatalf("timeline fetched %d times, want exactly once", got)
	}
	if got := recSpent(t, fx.costs); got != recRate*372 {
		t.Fatalf("spent %d, want exactly one settle of %d", got, recRate*372)
	}
	if reserved := recReserved(t, fx.costs); reserved != 0 {
		t.Fatalf("held %d after two reconcilers, want none held", reserved)
	}
}

// orphanFileWriter writes one artifact under its claimed id and returns
// before the media row. contentType selects which artifact. keep is a
// prefix length, and zero writes the whole stream. Every other call uses
// the real store.
type orphanFileWriter struct {
	dir         string
	inner       *mediastore.Store
	contentType string
	keep        int
	mu          sync.Mutex
	orphaned    bool
}

func (w *orphanFileWriter) Persist(ctx context.Context, src io.Reader, put mediastore.Put) (string, error) {
	return w.inner.Persist(ctx, src, put)
}

func (w *orphanFileWriter) PersistWithID(ctx context.Context, blobID string, src io.Reader, put mediastore.Put) error {
	raw, err := io.ReadAll(src)
	if err != nil {
		return err
	}
	w.mu.Lock()
	orphanThis := !w.orphaned && put.ContentType == w.contentType
	if orphanThis {
		w.orphaned = true
	}
	w.mu.Unlock()
	if !orphanThis {
		return w.inner.PersistWithID(ctx, blobID, bytes.NewReader(raw), put)
	}
	body := raw
	if w.keep > 0 && w.keep < len(raw) {
		body = raw[:w.keep]
	}
	if err := os.WriteFile(filepath.Join(w.dir, blobID), body, 0o644); err != nil {
		return err
	}
	return errors.New("artifact file landed before the media row")
}

// TestReconcileOrphanFileStoresOneRecording leaves a claimed id and a
// file, and no media row. That is the gap after the file is created and
// before the row commits. A second reconciler stores one private
// recording and one private timeline. A short file is replaced with the
// download. A finished file is kept. Spent does not move again.
func TestReconcileOrphanFileStoresOneRecording(t *testing.T) {
	t.Run("complete recording", func(t *testing.T) {
		reconcileOrphanFile(t, "audio/ogg", 0)
	})
	t.Run("complete timeline", func(t *testing.T) {
		reconcileOrphanFile(t, "application/json", 0)
	})
	t.Run("partial recording", func(t *testing.T) {
		reconcileOrphanFile(t, "audio/ogg", 16)
	})
}

func reconcileOrphanFile(t *testing.T, contentType string, keep int) {
	t.Helper()
	fx := newRecFixture(t)
	in := fx.mintSession("prov-a")
	const recordingURL = "https://artifacts.example/rec-a.ogg"
	const timelineURL = "https://artifacts.example/tl-a.json"
	fetcher := &countingFetcher{blobs: map[string][]byte{
		recordingURL: recAudioA,
		timelineURL:  recTimelineA,
	}}
	writer := &orphanFileWriter{
		dir: fx.mediaDir, inner: fx.media, contentType: contentType, keep: keep,
	}
	fx.rec.artifacts = fetcher
	fx.rec.media = writer

	if _, err := fx.rec.Reconcile(t.Context(), in); err == nil {
		t.Fatal("reconcile succeeded, want the gap before the media row")
	}
	settled, recordingID, timelineID := recClaimState(t, fx.db, in.SessionID)
	if !settled || recordingID == "" {
		t.Fatalf("settled %v recording %q, want a settled claim and a recording id", settled, recordingID)
	}
	if contentType == "application/json" && timelineID == "" {
		t.Fatal("timeline id is empty, want the claim set before the missing row")
	}
	if _, err := os.Stat(filepath.Join(fx.mediaDir, claimedOrphanID(contentType, recordingID, timelineID))); err != nil {
		t.Fatalf("orphan file: %v", err)
	}
	var audioRows int
	if err := fx.db.Writer().QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM media WHERE id = ?`, recordingID).Scan(&audioRows); err != nil {
		t.Fatalf("count recording row: %v", err)
	}
	if contentType == "audio/ogg" && audioRows != 0 {
		t.Fatalf("recording row count %d, want none before the second reconciler", audioRows)
	}
	spent := recSpent(t, fx.costs)
	if spent != recRate*372 {
		t.Fatalf("spent %d, want one settle of %d", spent, recRate*372)
	}

	next := fx.openReconciler(fx.media, fetcher)
	if _, err := next.Reconcile(t.Context(), in); err != nil {
		held := recPrivateTypes(t, fx.db, in.EpisodeID)
		t.Fatalf("second reconcile: %v, episode media %v", err, held)
	}
	_, recordingAfter, timelineAfter := recClaimState(t, fx.db, in.SessionID)
	if recordingAfter != recordingID {
		t.Fatalf("recording id changed from %s to %s", recordingID, recordingAfter)
	}
	if timelineID != "" && timelineAfter != timelineID {
		t.Fatalf("timeline id changed from %s to %s", timelineID, timelineAfter)
	}
	if timelineAfter == "" {
		t.Fatal("timeline id is empty after the second reconciler")
	}
	held := recPrivateTypes(t, fx.db, in.EpisodeID)
	if held["audio/ogg"] != 1 || held["application/json"] != 1 || len(held) != 2 {
		t.Fatalf("episode media %v, want one recording and one timeline", held)
	}
	assertClaimedBlob(t, fx.db, fx.mediaDir, recordingAfter, "audio/ogg", recAudioA)
	assertClaimedBlob(t, fx.db, fx.mediaDir, timelineAfter, "application/json", recTimelineA)
	if got := recSpent(t, fx.costs); got != spent {
		t.Fatalf("spent moved %d to %d on the second reconciler", spent, got)
	}
}

func claimedOrphanID(contentType, recordingID, timelineID string) string {
	if contentType == "application/json" {
		return timelineID
	}
	return recordingID
}

func assertClaimedBlob(t *testing.T, db *sqlite.DB, dir, blobID, contentType string, want []byte) {
	t.Helper()
	var gotType, visibility, owner string
	var size int64
	if err := db.Writer().QueryRowContext(t.Context(),
		`SELECT content_type, visibility, owner, size_bytes FROM media WHERE id = ?`, blobID).
		Scan(&gotType, &visibility, &owner, &size); err != nil {
		t.Fatalf("media %s: %v", blobID, err)
	}
	if gotType != contentType || visibility != "private" || owner != recOwner {
		t.Fatalf("media %s type %s visibility %s owner %s", blobID, gotType, visibility, owner)
	}
	raw, err := os.ReadFile(filepath.Join(dir, blobID))
	if err != nil {
		t.Fatalf("read %s: %v", blobID, err)
	}
	if int64(len(raw)) != size || !bytes.Equal(raw, want) {
		t.Fatalf("blob %s is %d bytes, row %d, want %d artifact bytes", blobID, len(raw), size, len(want))
	}
}

// settleDelayReader stays open for a fixed number of reads, then reports
// a duration. It counts every read and stop, so a test pins exactly how
// hard the reconcile worked for its settle.
type settleDelayReader struct {
	mu        sync.Mutex
	openReads int
	reads     int
	stopped   bool
	stops     int
	ready     ProviderSession
}

func (s *settleDelayReader) ReadSession(context.Context, string) (ProviderSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	if s.reads <= s.openReads {
		return ProviderSession{}, ErrSessionOpen
	}
	return s.ready, nil
}

func (s *settleDelayReader) StopSocket(context.Context, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stops++
	s.stopped = true
	return nil
}

func (s *settleDelayReader) stopCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stops
}

// waitRecorder returns at once and keeps every duration it saw.
type waitRecorder struct {
	mu   sync.Mutex
	seen []time.Duration
}

func (w *waitRecorder) wait(_ context.Context, d time.Duration) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seen = append(w.seen, d)
	return nil
}

func (w *waitRecorder) durations() []time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]time.Duration(nil), w.seen...)
}

func openSettleReconciler(t *testing.T, fx *recFixture, sessions SessionReader, waits *waitRecorder) *Reconciler {
	t.Helper()
	rec, err := NewReconciler(ReconcilerConfig{
		DB:       fx.db,
		Sessions: sessions,
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
		Wait:                 waits.wait,
	})
	if err != nil {
		t.Fatalf("new reconciler: %v", err)
	}
	return rec
}

func equalDurations(got, want []time.Duration) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestReconcileWaitsForProviderClose settles a session whose provider
// still reports open twice after the socket stops. The settle stores
// both artifacts, the claim row reads settled with both media ids, the
// socket stops once, and the waits saw 500 ms then 1 s.
func TestReconcileWaitsForProviderClose(t *testing.T) {
	fx := newRecFixture(t)
	in := fx.mintSession("prov-wait")
	reader := &settleDelayReader{
		openReads: 2,
		ready: ProviderSession{
			ID:              "prov-wait",
			DurationSeconds: 45,
			RecordingURL:    "https://artifacts.example/rec-a.ogg",
			TimelineURL:     "https://artifacts.example/tl-a.json",
		},
	}
	waits := &waitRecorder{}
	rec := openSettleReconciler(t, fx, reader, waits)
	res, err := rec.Reconcile(t.Context(), in)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.ConnectedSeconds != 45 || res.Cost != recRate*45 {
		t.Fatalf("settled %+v, want 45 seconds at %d", res, recRate*45)
	}
	if res.RecordingMediaID == "" || res.TimelineMediaID == "" {
		t.Fatalf("result %+v stored no recording and timeline pair", res)
	}
	settled, recordingID, timelineID := recClaimState(t, fx.db, in.SessionID)
	if !settled {
		t.Fatal("claim is not settled after the close waited out")
	}
	if recordingID != res.RecordingMediaID || timelineID != res.TimelineMediaID {
		t.Fatalf("claim ids %q/%q differ from result %+v", recordingID, timelineID, res)
	}
	if got := reader.stopCount(); got != 1 {
		t.Fatalf("socket stopped %d times, want exactly once", got)
	}
	if got := waits.durations(); !equalDurations(got, []time.Duration{500 * time.Millisecond, time.Second}) {
		t.Fatalf("waits %v, want 500ms then 1s", got)
	}
	if got := recSpent(t, fx.costs); got != recRate*45 {
		t.Fatalf("spent %d, want exactly one settle at %d", got, recRate*45)
	}
	assertClaimedBlob(t, fx.db, fx.mediaDir, res.RecordingMediaID, "audio/ogg", recAudioA)
	assertClaimedBlob(t, fx.db, fx.mediaDir, res.TimelineMediaID, "application/json", recTimelineA)
}

// TestReconcileOpenAfterBackoffStaysUnsettled leaves the provider open
// through every backoff step. The reconcile returns an error wrapping
// the open session sentinel, the claim row stays unsettled, and the
// waits saw all six steps. A single immediate retry would settle
// nothing here either, so this pins the loop, not one read.
func TestReconcileOpenAfterBackoffStaysUnsettled(t *testing.T) {
	fx := newRecFixture(t)
	in := fx.mintSession("prov-never")
	reader := &settleDelayReader{openReads: 100}
	waits := &waitRecorder{}
	rec := openSettleReconciler(t, fx, reader, waits)
	if _, err := rec.Reconcile(t.Context(), in); !errors.Is(err, ErrSessionOpen) {
		t.Fatalf("reconcile err %v, want the open session sentinel", err)
	}
	settled, recordingID, timelineID := recClaimState(t, fx.db, in.SessionID)
	if settled {
		t.Fatal("claim settled on a session that never closed")
	}
	if recordingID != "" || timelineID != "" {
		t.Fatalf("claim holds ids %q/%q with no duration read", recordingID, timelineID)
	}
	want := []time.Duration{
		500 * time.Millisecond, time.Second, 2 * time.Second,
		4 * time.Second, 8 * time.Second, 8 * time.Second,
	}
	if got := waits.durations(); !equalDurations(got, want) {
		t.Fatalf("waits %v, want all six backoff steps", got)
	}
	if got := reader.stopCount(); got != 1 {
		t.Fatalf("socket stopped %d times, want exactly once", got)
	}
	if got := recSpent(t, fx.costs); got != 0 {
		t.Fatalf("spent %d on an unsettled session, want zero", got)
	}
	if held := recReserved(t, fx.costs); held != fx.estimate {
		t.Fatalf("held %d, want the untouched estimate", held)
	}
}

// TestReconcileSettledSessionSkipsStopAndWait runs a second reconcile
// on a settled session. The settled path returns the stored result
// without stopping the socket or waiting again.
func TestReconcileSettledSessionSkipsStopAndWait(t *testing.T) {
	fx := newRecFixture(t)
	in := fx.mintSession("prov-wait")
	reader := &settleDelayReader{
		openReads: 1,
		ready: ProviderSession{
			ID:              "prov-wait",
			DurationSeconds: 45,
			RecordingURL:    "https://artifacts.example/rec-a.ogg",
			TimelineURL:     "https://artifacts.example/tl-a.json",
		},
	}
	waits := &waitRecorder{}
	rec := openSettleReconciler(t, fx, reader, waits)
	first, err := rec.Reconcile(t.Context(), in)
	if err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	stops := reader.stopCount()
	waitsSeen := len(waits.durations())
	second, err := rec.Reconcile(t.Context(), in)
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if second != first {
		t.Fatalf("second result %+v differs from %+v", second, first)
	}
	if got := reader.stopCount(); got != stops {
		t.Fatalf("socket stopped %d times, want no second stop past %d", got, stops)
	}
	if got := len(waits.durations()); got != waitsSeen {
		t.Fatalf("waits ran %d times, want no second wait past %d", got, waitsSeen)
	}
}
