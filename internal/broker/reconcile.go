// Package broker starts live sessions behind seven ordered checks.
//
// This file settles them. After a session ends, reconciliation reads the
// real connected seconds from the provider and settles the mint hold to
// that cost. It closes the lease at the provider number. It persists the
// stereo recording and the timeline privately, because artifact URLs expire.
// The provider read repeats safely, so the job kind is idempotent. The
// money settle runs exactly once, guarded by a durable claim row this file
// owns. Each artifact id is claimed in that row before its bytes are fetched,
// so another pass cannot store a second copy after a crash or from another
// reconciler. A file that lands without its row is indexed when it matches
// the download, and replaced when it does not. The per session lock only
// orders calls that share one value.
// An ambiguous resume never settles twice. It stops for review.
package broker

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/nrynss/keel/cost"
	costsqlitestore "github.com/nrynss/keel/cost/sqlitestore"
	"github.com/nrynss/keel/id"
	"github.com/nrynss/keel/job"
	"github.com/nrynss/keel/lease"
	"github.com/nrynss/keel/mediastore"
	"github.com/nrynss/keel/sqlite"
)

// KindName is the job kind reconciliation runs under. The wiring registers
// it with Kind, so a restart resumes an unfinished settle instead of
// dropping it.
const KindName = "reconcile"

// DefaultMarginSeconds is the grace past the session cap before a session
// counts as over cap. A session past cap plus margin should never happen,
// so it alerts.
const DefaultMarginSeconds = 60

// DefaultRecordingContentType is the media type the stereo recording lands
// under. The provider keeps its own copy beside ours.
const DefaultRecordingContentType = "audio/ogg"

// DefaultTimelineContentType is the media type the timeline lands under.
// The timeline arrives as JSON beside the recording.
const DefaultTimelineContentType = "application/json"

// DefaultFetchMaxBytes caps one artifact download. A three hour stereo
// recording fits with room, and anything larger is a provider surprise
// worth refusing rather than streaming to disk unbounded.
const DefaultFetchMaxBytes = 512 << 20

// Sentinels, one per failure condition.
var (
	// ErrRead reports a provider session read or artifact fetch that
	// failed. Reads repeat safely, so the job retries them.
	ErrRead = errors.New("broker: read provider session")
	// ErrSessionOpen reports a provider session that is still running.
	// It wraps ErrRead, so a caller that only retries reads still matches.
	// The reconcile stops the live socket and then reads again after each
	// backoff step until the provider reports a duration.
	ErrSessionOpen = fmt.Errorf("broker: provider session is still open: %w", ErrRead)
	// ErrSettle reports a budget settle or lease close that failed after
	// the claim was taken. The claim stays, so a resume reviews instead
	// of spending twice.
	ErrSettle = errors.New("broker: settle session spend")
	// ErrState reports a durable write the reconciler owns that failed:
	// its claim row, the session row update, or the media persist.
	ErrState = errors.New("broker: write reconcile state")
)

// ProviderSession is one provider session read. DurationSeconds is the
// connected time the settle prices. RecordingURL names the stereo
// recording, TimelineURL the timeline. Both expire, so both artifacts
// land in the media store on receipt and the URLs stay as metadata.
type ProviderSession struct {
	// ID is the provider session id that was read.
	ID string `json:"id"`
	// DurationSeconds is the connected time in seconds.
	DurationSeconds int `json:"duration_seconds"`
	// RecordingURL is the expiring stereo recording URL, or empty.
	RecordingURL string `json:"recording_url"`
	// TimelineURL is the expiring timeline URL, or empty.
	TimelineURL string `json:"timeline_url"`
}

// providerPayload decodes the recorded session body. It is flat on
// purpose. One shape stays pinned in the test, and the live probe checks
// it against the real API before anything depends on a second shape.
type providerPayload struct {
	ID              string `json:"id"`
	DurationSeconds *int   `json:"duration_seconds"`
	RecordingURL    string `json:"recording_url"`
	TimelineURL     string `json:"timeline_url"`
}

// ParseProviderSession decodes one recorded session body. It reports
// ErrInvalid for a body with no usable duration, because a settle prices
// nothing without one.
func ParseProviderSession(data []byte) (ProviderSession, error) {
	var raw providerPayload
	if err := json.Unmarshal(data, &raw); err != nil {
		return ProviderSession{}, fmt.Errorf("broker: decode provider session: %w", ErrInvalid)
	}
	if raw.DurationSeconds == nil || *raw.DurationSeconds < 0 {
		return ProviderSession{}, fmt.Errorf("broker: decode provider session: %w: duration is missing", ErrInvalid)
	}
	return ProviderSession{
		ID:              raw.ID,
		DurationSeconds: *raw.DurationSeconds,
		RecordingURL:    raw.RecordingURL,
		TimelineURL:     raw.TimelineURL,
	}, nil
}

// SessionReader reads one provider session. The provider package
// implements it over GET on its sessions endpoint. The reconciler declares
// the seam, so this file never carries the provider host or key.
type SessionReader interface {
	// ReadSession returns the provider session for a provider session id.
	ReadSession(ctx context.Context, providerSessionID string) (ProviderSession, error)
}

// ArtifactFetcher downloads one artifact URL. Artifact URLs carry their
// own grant and expire, so the fetch needs no provider key.
type ArtifactFetcher interface {
	// Fetch returns the artifact bytes as a stream. The caller closes it.
	Fetch(ctx context.Context, artifactURL string) (io.ReadCloser, error)
}

// Settler books the real cost against the owner and global ceilings and
// frees the mint time hold. The keyed budget implements it.
type Settler interface {
	// Settle books actual spend and frees the hold r names.
	Settle(ctx context.Context, owner string, r costsqlitestore.Reservation, actual cost.Price) error
}

// LeaseSettler closes leases at the reported price and applies the
// provider number as the truth. The lease manager implements it.
type LeaseSettler interface {
	// Close ends the open lease at the reported price.
	Close(ctx context.Context, id string, price cost.Price) (lease.Lease, error)
	// Reconcile applies the provider price to the closed lease.
	Reconcile(ctx context.Context, id string, provider cost.Price) (lease.Lease, error)
	// Inspect reads the lease, expiring it past its cap.
	Inspect(ctx context.Context, id string) (lease.Lease, error)
}

// SessionStore records the settled duration on the session row. The diary
// implements it once the store owner adds the write.
type SessionStore interface {
	// SetConnectedSeconds writes the settled duration on one session row.
	SetConnectedSeconds(ctx context.Context, sessionID string, connectedSeconds int) error
}

// MediaWriter persists bytes privately. The media store implements it.
// A writer that can store under a reserved id lets the claim row name
// the blob before those bytes commit.
type MediaWriter interface {
	// Persist writes src as a new blob and returns its id.
	Persist(ctx context.Context, src io.Reader, blob mediastore.Put) (string, error)
}

// idMediaWriter stores bytes under an id the caller already recorded.
// Persist mints its id while the row commits, which is too late to
// survive a crash between that commit and a later claim update.
type idMediaWriter interface {
	// PersistWithID writes src as blobID. The id must already be claimed.
	PersistWithID(ctx context.Context, blobID string, src io.Reader, blob mediastore.Put) error
}

// AlertKind names why the reconciler raised an alert.
type AlertKind string

// Alert kinds. OverCap should never fire. NeedsReview fires when an
// ambiguous resume leaves money unsettled for an operator. SweepOpen fires
// when the sweep settles a session the provider still reports open, so
// spend may keep running past the settle.
const (
	// AlertOverCap marks a session connected past its cap plus margin.
	AlertOverCap AlertKind = "over_cap"
	// AlertNeedsReview marks a session an operator should check. It fires
	// when a resume cannot prove the settle state and nothing settled, and
	// it also fires after the sweep charges the full cap for a session the
	// provider record cannot price. The detail names which case fired.
	AlertNeedsReview AlertKind = "needs_review"
	// AlertSweepOpen marks a session the sweep settled while the provider
	// still reported it open. The books close at the accrued cost, and
	// the meter may run on.
	AlertSweepOpen AlertKind = "sweep_open"
)

// Alert is one operator signal from reconciliation. Alerts are advisory.
// Money correctness never depends on their delivery.
type Alert struct {
	// Kind names why the alert fired.
	Kind AlertKind `json:"kind"`
	// SessionID is the diary session the alert belongs to.
	SessionID string `json:"session_id"`
	// OwnerID is the session owner.
	OwnerID string `json:"owner_id"`
	// Detail carries the numbers in plain words.
	Detail string `json:"detail"`
}

// Alerter delivers operator alerts. A nil Alerter records alerts on the
// result only.
type Alerter interface {
	// Report delivers one alert.
	Report(ctx context.Context, alert Alert) error
}

var (
	_ Settler       = (*costsqlitestore.KeyedBudget)(nil)
	_ LeaseSettler  = (*lease.Manager)(nil)
	_ MediaWriter   = (*mediastore.Store)(nil)
	_ idMediaWriter = (*mediastore.Store)(nil)
)

// Input is one reconciliation. The caller carries the mint time linkage,
// because no diary column links a lease row to a session row yet. The
// wiring passes the ids the mint returned. Job progress carries the input
// as JSON, so a resume rebuilds the read from the record alone.
type Input struct {
	// SessionID is the diary session row.
	SessionID string `json:"session_id"`
	// OwnerID is the session owner.
	OwnerID string `json:"owner_id"`
	// EpisodeID is the episode the session recorded.
	EpisodeID string `json:"episode_id"`
	// ProviderSessionID is the provider session to read.
	ProviderSessionID string `json:"provider_session_id"`
	// LeaseID is the lease the mint opened.
	LeaseID string `json:"lease_id"`
	// Reservation is the mint time budget hold to settle.
	Reservation costsqlitestore.Reservation `json:"reservation"`
	// TokenCapSeconds is the session cap the mint reserved for.
	TokenCapSeconds int `json:"token_cap_seconds"`
}

// Result is one settled reconciliation.
type Result struct {
	// SessionID is the diary session that was reconciled.
	SessionID string `json:"session_id"`
	// ConnectedSeconds is the provider reported duration.
	ConnectedSeconds int `json:"connected_seconds"`
	// Cost is the booked price at the session rate.
	Cost cost.Price `json:"cost_nd"`
	// OverCap reports a session past its cap plus margin.
	OverCap bool `json:"over_cap"`
	// RecordingMediaID is the persisted stereo recording, or empty when
	// the provider carried no recording URL.
	RecordingMediaID string `json:"recording_media_id"`
	// TimelineMediaID is the persisted timeline, or empty when the
	// provider carried no timeline URL.
	TimelineMediaID string `json:"timeline_media_id"`
	// TimelineURL keeps the expiring timeline URL as metadata.
	TimelineURL string `json:"timeline_url"`
	// NeedsReview reports an ambiguous resume that settled nothing, for
	// an operator to verify.
	NeedsReview bool `json:"needs_review"`
	// ReviewDetail explains a review in plain words.
	ReviewDetail string `json:"review_detail"`
	// AlertError carries an alert delivery failure, if any. The settle
	// stands regardless.
	AlertError string `json:"alert_error"`
}

// ReconcilerConfig carries everything the reconciler needs. The zero value
// is not usable.
type ReconcilerConfig struct {
	// DB holds the claim rows beside every other store. It must not be nil.
	DB *sqlite.DB
	// Sessions reads provider sessions. It must not be nil.
	Sessions SessionReader
	// Artifacts downloads artifact URLs. It must not be nil.
	Artifacts ArtifactFetcher
	// Budgets settles the mint time holds. It must not be nil.
	Budgets Settler
	// Leases closes and reconciles the session leases. It must not be nil.
	Leases LeaseSettler
	// Diary writes the settled duration. It must not be nil.
	Diary SessionStore
	// Media persists the stereo recording and the timeline. It must not be nil.
	Media MediaWriter
	// Alerter delivers over cap and review alerts. Nil records them on
	// the result only.
	Alerter Alerter
	// MarginSeconds is the grace past the cap before OverCap fires. Zero
	// flags past the cap exactly. Negative is invalid.
	MarginSeconds int
	// RecordingContentType is the media type the recording lands under.
	// Empty means the default.
	RecordingContentType string
	// TimelineContentType is the media type the timeline lands under.
	// Empty means the default.
	TimelineContentType string
	// FetchMaxBytes caps one artifact download. Zero or negative means
	// the default.
	FetchMaxBytes int64
	// Wait pauses between provider reads while a closing session still
	// reports open. Nil uses a real timer that returns ctx.Err() when
	// the context ends. Tests pass a Wait that returns at once and
	// records each duration.
	Wait func(ctx context.Context, d time.Duration) error
}

// Reconciler settles live sessions against provider truth. Create it with
// New, because the zero value holds no stores. A Reconciler is safe for
// concurrent use. Calls that share one value take one session at a time.
// A second value claims artifact ids in the row instead, so both still
// store one recording and one timeline.
type Reconciler struct {
	db           *sqlite.DB
	sessions     SessionReader
	artifacts    ArtifactFetcher
	budgets      Settler
	leases       LeaseSettler
	diary        SessionStore
	media        MediaWriter
	alerter      Alerter
	margin       int
	contentType  string
	timelineType string
	maxBytes     int64
	wait         func(ctx context.Context, d time.Duration) error
	mu           sync.Mutex
	guards       map[string]*sessionGuard
}

// sessionGuard serializes one session across reconcile jobs. The waiter
// count drops the map entry once the last holder leaves, so the map
// holds running sessions only.
type sessionGuard struct {
	mu      sync.Mutex
	waiters int
}

// hold returns the guard for one session and counts this holder. The
// caller locks the guard and drops it with release.
func (r *Reconciler) hold(sessionID string) *sessionGuard {
	r.mu.Lock()
	defer r.mu.Unlock()
	guard, ok := r.guards[sessionID]
	if !ok {
		guard = &sessionGuard{}
		r.guards[sessionID] = guard
	}
	guard.waiters++
	return guard
}

// release unlocks the guard and drops it once no holder remains.
func (r *Reconciler) release(sessionID string, guard *sessionGuard) {
	guard.mu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	guard.waiters--
	if guard.waiters == 0 {
		delete(r.guards, sessionID)
	}
}

// claimRow is one durable settle claim.
type claimRow struct {
	claimed          bool
	settled          bool
	connectedSeconds int
	overCap          bool
	overCapAlerted   bool
	recordingMediaID string
	timelineMediaID  string
	recordingURL     string
	timelineURL      string
}

// NewReconciler validates cfg, creates the claim table, and returns the
// reconciler.
func NewReconciler(cfg ReconcilerConfig) (*Reconciler, error) {
	if cfg.DB == nil {
		return nil, fmt.Errorf("broker: new reconciler: %w: database must not be nil", ErrInvalid)
	}
	if cfg.Sessions == nil {
		return nil, fmt.Errorf("broker: new reconciler: %w: session reader must not be nil", ErrInvalid)
	}
	if cfg.Artifacts == nil {
		return nil, fmt.Errorf("broker: new reconciler: %w: artifact fetcher must not be nil", ErrInvalid)
	}
	if cfg.Budgets == nil {
		return nil, fmt.Errorf("broker: new reconciler: %w: budgets must not be nil", ErrInvalid)
	}
	if cfg.Leases == nil {
		return nil, fmt.Errorf("broker: new reconciler: %w: leases must not be nil", ErrInvalid)
	}
	if cfg.Diary == nil {
		return nil, fmt.Errorf("broker: new reconciler: %w: diary must not be nil", ErrInvalid)
	}
	if cfg.Media == nil {
		return nil, fmt.Errorf("broker: new reconciler: %w: media must not be nil", ErrInvalid)
	}
	if cfg.MarginSeconds < 0 {
		return nil, fmt.Errorf("broker: new reconciler: %w: margin must not be negative", ErrInvalid)
	}
	contentType := cfg.RecordingContentType
	if contentType == "" {
		contentType = DefaultRecordingContentType
	}
	timelineType := cfg.TimelineContentType
	if timelineType == "" {
		timelineType = DefaultTimelineContentType
	}
	maxBytes := cfg.FetchMaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultFetchMaxBytes
	}
	wait := cfg.Wait
	if wait == nil {
		wait = func(ctx context.Context, d time.Duration) error {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		}
	}
	r := &Reconciler{
		db:           cfg.DB,
		sessions:     cfg.Sessions,
		artifacts:    cfg.Artifacts,
		budgets:      cfg.Budgets,
		leases:       cfg.Leases,
		diary:        cfg.Diary,
		media:        cfg.Media,
		alerter:      cfg.Alerter,
		margin:       cfg.MarginSeconds,
		contentType:  contentType,
		timelineType: timelineType,
		maxBytes:     maxBytes,
		wait:         wait,
		guards:       map[string]*sessionGuard{},
	}
	const schema = `CREATE TABLE IF NOT EXISTS reconcile_state (
		session_id TEXT PRIMARY KEY,
		claimed INTEGER NOT NULL DEFAULT 0,
		settled INTEGER NOT NULL DEFAULT 0,
		connected_seconds INTEGER NOT NULL DEFAULT 0,
		cost_nd INTEGER NOT NULL DEFAULT 0,
		over_cap INTEGER NOT NULL DEFAULT 0,
		over_cap_alerted INTEGER NOT NULL DEFAULT 0,
		recording_media_id TEXT NOT NULL DEFAULT '',
		timeline_media_id TEXT NOT NULL DEFAULT '',
		recording_url TEXT NOT NULL DEFAULT '',
		timeline_url TEXT NOT NULL DEFAULT '',
		end_pending INTEGER NOT NULL DEFAULT 0,
		artifacts_pending INTEGER NOT NULL DEFAULT 0,
		updated_at INTEGER NOT NULL DEFAULT 0
	)`
	if _, err := r.db.Writer().ExecContext(context.Background(), schema); err != nil {
		return nil, fmt.Errorf("broker: create reconcile state: %w", ErrState)
	}
	if err := r.ensureColumn(context.Background(),
		"over_cap_alerted", "over_cap_alerted INTEGER NOT NULL DEFAULT 0"); err != nil {
		return nil, err
	}
	if err := r.ensureColumn(context.Background(),
		"timeline_media_id", "timeline_media_id TEXT NOT NULL DEFAULT ''"); err != nil {
		return nil, err
	}
	if err := r.ensureColumn(context.Background(),
		"end_pending", "end_pending INTEGER NOT NULL DEFAULT 0"); err != nil {
		return nil, err
	}
	if err := r.ensureColumn(context.Background(),
		"artifacts_pending", "artifacts_pending INTEGER NOT NULL DEFAULT 0"); err != nil {
		return nil, err
	}
	return r, nil
}

// ensureColumn adds one column to claim tables written before it
// existed. Fresh tables already carry every column from the schema, so
// the common path changes nothing.
func (r *Reconciler) ensureColumn(ctx context.Context, name, definition string) error {
	rows, err := r.db.Reader().QueryContext(ctx, `PRAGMA table_info(reconcile_state)`)
	if err != nil {
		return fmt.Errorf("broker: read reconcile columns: %w", ErrState)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var column, ctype string
		var dflt any
		if err := rows.Scan(&cid, &column, &ctype, &notNull, &dflt, &pk); err != nil {
			return fmt.Errorf("broker: read reconcile columns: %w", ErrState)
		}
		if column == name {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("broker: read reconcile columns: %w", ErrState)
	}
	if _, err := r.db.Writer().ExecContext(ctx,
		`ALTER TABLE reconcile_state ADD COLUMN `+definition); err != nil {
		return fmt.Errorf("broker: add reconcile column: %w", ErrState)
	}
	return nil
}

// Kind returns the job kind reconciliation runs under. Reads repeat
// safely, so the kind is idempotent with three attempts, and Resume
// rebuilds the read from the interrupted record.
func (r *Reconciler) Kind() job.Kind {
	return job.Kind{
		Idempotent:  true,
		MaxAttempts: 3,
		Resume:      r.Resume,
	}
}

// Resume rebuilds the work for an interrupted reconcile record. The input
// rides on the progress snapshot the attempt wrote before its provider
// read, so the rebuild needs nothing but the record.
func (r *Reconciler) Resume(rec job.Record) (job.Func, error) {
	if len(rec.Progress.Detail) == 0 {
		return nil, fmt.Errorf("broker: resume reconcile: %w: record carries no input", ErrInvalid)
	}
	var in Input
	if err := json.Unmarshal(rec.Progress.Detail, &in); err != nil {
		return nil, fmt.Errorf("broker: resume reconcile: %w: record input is corrupt", ErrInvalid)
	}
	return r.runFunc(in), nil
}

// runFunc wraps one reconciliation as job work. It reports the input
// before the provider read, so any crash after that point resumes with
// the linkage intact. Later stages repeat the input for the same reason.
func (r *Reconciler) runFunc(in Input) job.Func {
	raw, err := json.Marshal(in)
	if err != nil {
		return func(context.Context, func(job.Progress)) ([]byte, error) {
			return nil, fmt.Errorf("broker: encode reconcile input: %w", ErrInvalid)
		}
	}
	detail := json.RawMessage(raw)
	return func(ctx context.Context, progress func(job.Progress)) ([]byte, error) {
		progress(job.Progress{Stage: "read", Detail: detail})
		res, err := r.Reconcile(ctx, in)
		if err != nil {
			progress(job.Progress{Stage: "error", Detail: detail})
			data, _ := json.Marshal(res)
			return data, err
		}
		progress(job.Progress{Stage: "artifacts", Detail: detail})
		data, merr := json.Marshal(res)
		if merr != nil {
			return nil, fmt.Errorf("broker: encode reconcile result: %w", ErrState)
		}
		return data, nil
	}
}

// RunFunc builds the job work for one reconciliation. The session end
// path starts it after the provider close lands, and Resume rebuilds
// it after a restart. One session settles through one job at a time,
// and the claim inside keeps money at most once.
func (r *Reconciler) RunFunc(in Input) job.Func {
	return r.runFunc(in)
}

// validate rejects an input the reconciler cannot settle. The provider
// id must name a real call, because the read prices that record.
func validate(in Input) error {
	if in.SessionID == "" || in.OwnerID == "" || in.EpisodeID == "" {
		return fmt.Errorf("broker: reconcile: %w: session, owner, and episode must not be empty", ErrInvalid)
	}
	if in.ProviderSessionID == "" {
		return fmt.Errorf("broker: reconcile: %w: provider session id must not be empty", ErrInvalid)
	}
	if in.LeaseID == "" {
		return fmt.Errorf("broker: reconcile: %w: lease id must not be empty", ErrInvalid)
	}
	if in.Reservation.ID == "" {
		return fmt.Errorf("broker: reconcile: %w: reservation must name a hold", ErrInvalid)
	}
	if in.TokenCapSeconds <= 0 {
		return fmt.Errorf("broker: reconcile: %w: session cap must be positive", ErrInvalid)
	}
	return nil
}

// validateAbandoned rejects an input the abandoned settle cannot price.
// The provider id may be empty here: a session the browser never reported
// still bills its cap, and the caller passes that cap as the duration.
// Every other linkage must hold, as in validate.
func validateAbandoned(in Input) error {
	if in.SessionID == "" || in.OwnerID == "" || in.EpisodeID == "" {
		return fmt.Errorf("broker: reconcile: %w: session, owner, and episode must not be empty", ErrInvalid)
	}
	if in.LeaseID == "" {
		return fmt.Errorf("broker: reconcile: %w: lease id must not be empty", ErrInvalid)
	}
	if in.Reservation.ID == "" {
		return fmt.Errorf("broker: reconcile: %w: reservation must name a hold", ErrInvalid)
	}
	if in.TokenCapSeconds <= 0 {
		return fmt.Errorf("broker: reconcile: %w: session cap must be positive", ErrInvalid)
	}
	return nil
}

// priceForSeconds prices connected seconds at the session rate. It reports
// ErrInvalid past the int64 range, because a wrapped price books wrong.
func priceForSeconds(seconds int) (cost.Price, error) {
	if seconds < 0 {
		return 0, fmt.Errorf("broker: price %d seconds: %w: duration must not be negative", seconds, ErrInvalid)
	}
	price := ratePerSecondNanodollars * cost.Price(seconds)
	if cost.Price(seconds) != 0 && price/cost.Price(seconds) != ratePerSecondNanodollars {
		return 0, fmt.Errorf("broker: price %d seconds: %w: price leaves the int64 range", seconds, ErrInvalid)
	}
	return price, nil
}

// ensureRow inserts the claim row when absent. A present row stays as it
// is, so a resume finds the claim it left.
func (r *Reconciler) ensureRow(ctx context.Context, sessionID string) error {
	if _, err := r.db.Writer().ExecContext(ctx,
		`INSERT OR IGNORE INTO reconcile_state (session_id, updated_at) VALUES (?, ?)`,
		sessionID, time.Now().UnixMilli()); err != nil {
		return fmt.Errorf("broker: ensure reconcile row: %w", ErrState)
	}
	return nil
}

// readRow loads the claim row. The caller ensures it first.
func (r *Reconciler) readRow(ctx context.Context, sessionID string) (claimRow, error) {
	var row claimRow
	var claimed, settled, overCap, overCapAlerted int
	var seconds int
	var recordingID, timelineID, recordingURL, timelineURL string
	err := r.db.Reader().QueryRowContext(ctx,
		`SELECT claimed, settled, connected_seconds, over_cap, over_cap_alerted, recording_media_id,
			timeline_media_id, recording_url, timeline_url
		FROM reconcile_state WHERE session_id = ?`, sessionID).Scan(
		&claimed, &settled, &seconds, &overCap, &overCapAlerted, &recordingID, &timelineID, &recordingURL, &timelineURL)
	if err != nil {
		return claimRow{}, fmt.Errorf("broker: read reconcile row: %w", ErrState)
	}
	row.claimed = claimed != 0
	row.settled = settled != 0
	row.connectedSeconds = seconds
	row.overCap = overCap != 0
	row.overCapAlerted = overCapAlerted != 0
	row.recordingMediaID = recordingID
	row.timelineMediaID = timelineID
	row.recordingURL = recordingURL
	row.timelineURL = timelineURL
	return row, nil
}

// claim takes the settle claim when free. It reports false when another
// attempt holds it, so the loser reviews instead of spending twice.
func (r *Reconciler) claim(ctx context.Context, sessionID string) (bool, error) {
	done, err := r.db.Writer().ExecContext(ctx,
		`UPDATE reconcile_state SET claimed = 1, updated_at = ? WHERE session_id = ? AND claimed = 0`,
		time.Now().UnixMilli(), sessionID)
	if err != nil {
		return false, fmt.Errorf("broker: claim reconcile row: %w", ErrState)
	}
	won, err := done.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("broker: claim reconcile row: %w", ErrState)
	}
	return won == 1, nil
}

// unclaim frees the claim after a failed settle, so a retry may take it.
// It runs only while no money moved, which the caller guarantees.
func (r *Reconciler) unclaim(ctx context.Context, sessionID string) error {
	if _, err := r.db.Writer().ExecContext(ctx,
		`UPDATE reconcile_state SET claimed = 0, updated_at = ? WHERE session_id = ?`,
		time.Now().UnixMilli(), sessionID); err != nil {
		return fmt.Errorf("broker: unclaim reconcile row: %w", ErrState)
	}
	return nil
}

// markSettled records the settled money facts. Later runs skip the money
// path on this flag and finish the artifacts only.
func (r *Reconciler) markSettled(ctx context.Context, sessionID string, seconds int, price cost.Price, overCap bool, recordingURL, timelineURL string) error {
	over := 0
	if overCap {
		over = 1
	}
	if _, err := r.db.Writer().ExecContext(ctx,
		`UPDATE reconcile_state SET settled = 1, connected_seconds = ?, cost_nd = ?, over_cap = ?,
		recording_url = ?, timeline_url = ?, updated_at = ? WHERE session_id = ?`,
		seconds, int64(price), over, recordingURL, timelineURL, time.Now().UnixMilli(), sessionID); err != nil {
		return fmt.Errorf("broker: mark reconcile settled: %w", ErrState)
	}
	return nil
}

// claimArtifact records mediaID when that artifact id is still empty.
// The update runs before the fetch, so a crash after the blob commits
// still leaves the id for the next pass. False means a peer claimed it.
func (r *Reconciler) claimArtifact(ctx context.Context, sessionID, kind, mediaID string) (bool, error) {
	var query string
	switch kind {
	case "recording":
		query = `UPDATE reconcile_state SET recording_media_id = ?, updated_at = ? WHERE session_id = ? AND recording_media_id = ''`
	case "timeline":
		query = `UPDATE reconcile_state SET timeline_media_id = ?, updated_at = ? WHERE session_id = ? AND timeline_media_id = ''`
	default:
		return false, fmt.Errorf("broker: claim %s: %w: unknown artifact", kind, ErrState)
	}
	done, err := r.db.Writer().ExecContext(ctx, query, mediaID, time.Now().UnixMilli(), sessionID)
	if err != nil {
		return false, fmt.Errorf("broker: claim %s: %w: %w", kind, ErrState, err)
	}
	won, err := done.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("broker: claim %s: %w: %w", kind, ErrState, err)
	}
	return won == 1, nil
}

// artifactID reads the claimed id for kind from the writer connection.
// The claim update just committed there, and a pooled read can lag it.
func (r *Reconciler) artifactID(ctx context.Context, sessionID, kind string) (string, error) {
	var query string
	switch kind {
	case "recording":
		query = `SELECT recording_media_id FROM reconcile_state WHERE session_id = ?`
	case "timeline":
		query = `SELECT timeline_media_id FROM reconcile_state WHERE session_id = ?`
	default:
		return "", fmt.Errorf("broker: read %s id: %w: unknown artifact", kind, ErrState)
	}
	var mediaID string
	if err := r.db.Writer().QueryRowContext(ctx, query, sessionID).Scan(&mediaID); err != nil {
		return "", fmt.Errorf("broker: read %s id: %w: %w", kind, ErrState, err)
	}
	return mediaID, nil
}

// markOverCapAlerted records that the over cap alert fired. Later runs
// skip it, so one session alerts once no matter how often it retries.
func (r *Reconciler) markOverCapAlerted(ctx context.Context, sessionID string) error {
	if _, err := r.db.Writer().ExecContext(ctx,
		`UPDATE reconcile_state SET over_cap_alerted = 1, updated_at = ? WHERE session_id = ?`,
		time.Now().UnixMilli(), sessionID); err != nil {
		return fmt.Errorf("broker: mark reconcile alert: %w", ErrState)
	}
	return nil
}

// alert delivers one alert through the alerter when set. A delivery
// failure lands on the result, never on the settle.
func (r *Reconciler) alert(ctx context.Context, res *Result, kind AlertKind, owner, detail string) {
	if r.alerter == nil {
		return
	}
	if err := r.alerter.Report(ctx, Alert{Kind: kind, SessionID: res.SessionID, OwnerID: owner, Detail: detail}); err != nil {
		res.AlertError = err.Error()
	}
}

// review stops an ambiguous resume without moving money. The lease state
// could not prove the settle ran or not, so at most once wins and an
// operator verifies.
func (r *Reconciler) review(ctx context.Context, in Input, detail string) (Result, error) {
	res := Result{SessionID: in.SessionID, NeedsReview: true, ReviewDetail: detail}
	r.alert(ctx, &res, AlertNeedsReview, in.OwnerID, detail)
	return res, nil
}

// Reconcile settles one session against provider truth. It reads the
// provider duration and settles the mint hold to that cost on both
// ceilings. It closes the lease at the provider number, writes the
// duration on the session row, and persists the recording and timeline.
// Money settles at most once. Each artifact id is claimed before the
// fetch, so a repeat stores nothing and does not fetch again.
func (r *Reconciler) Reconcile(ctx context.Context, in Input) (Result, error) {
	if err := validate(in); err != nil {
		return Result{}, err
	}
	guard := r.hold(in.SessionID)
	guard.mu.Lock()
	defer r.release(in.SessionID, guard)
	if err := r.ensureRow(ctx, in.SessionID); err != nil {
		return Result{}, err
	}
	row, err := r.readRow(ctx, in.SessionID)
	if err != nil {
		return Result{}, err
	}
	if row.settled {
		return r.finishArtifacts(ctx, in, row)
	}
	if row.claimed {
		return r.resumeClaimed(ctx, in)
	}
	read, err := r.readForSettle(ctx, in.ProviderSessionID)
	if err != nil {
		return Result{}, err
	}
	if read.DurationSeconds < 0 {
		return Result{}, fmt.Errorf("broker: reconcile: %w: provider duration is negative", ErrRead)
	}
	return r.settleRead(ctx, in, read)
}

// AbandonedSession is one session the sweep found past its cap whose money
// never settled. DurationSeconds is the accrued cost to book: the provider
// duration for a session that already closed, or the elapsed open time for
// one that still runs. RecordingURL and TimelineURL ride along when the
// provider still names them.
type AbandonedSession struct {
	// DurationSeconds is the connected time to settle in seconds.
	DurationSeconds int
	// RecordingURL is the expiring stereo recording URL, or empty.
	RecordingURL string
	// TimelineURL is the expiring timeline URL, or empty.
	TimelineURL string
}

// ReconcileAbandoned settles one session the sweep found past its cap.
// The provider read cannot serve here: an open session reports no
// duration, and a deleted record reports nothing at all. The caller passes
// the accrued seconds it measured, and the same claim guard, money path
// and artifact tail run as a normal reconcile. A negative duration
// reports ErrRead, because a settle prices nothing without one.
func (r *Reconciler) ReconcileAbandoned(ctx context.Context, in Input, abandoned AbandonedSession) (Result, error) {
	if err := validateAbandoned(in); err != nil {
		return Result{}, err
	}
	if abandoned.DurationSeconds < 0 {
		return Result{}, fmt.Errorf("broker: reconcile abandoned: %w: provider duration is negative", ErrRead)
	}
	guard := r.hold(in.SessionID)
	guard.mu.Lock()
	defer r.release(in.SessionID, guard)
	if err := r.ensureRow(ctx, in.SessionID); err != nil {
		return Result{}, err
	}
	row, err := r.readRow(ctx, in.SessionID)
	if err != nil {
		return Result{}, err
	}
	if row.settled {
		return r.finishArtifacts(ctx, in, row)
	}
	if row.claimed {
		return r.resumeClaimed(ctx, in)
	}
	return r.settleRead(ctx, in, ProviderSession{
		ID:              in.ProviderSessionID,
		DurationSeconds: abandoned.DurationSeconds,
		RecordingURL:    abandoned.RecordingURL,
		TimelineURL:     abandoned.TimelineURL,
	})
}

// IsSettled reports whether the session already settled. The sweep does
// not move money for a settled session. A failed provider end stays
// retryable on its own flag.
func (r *Reconciler) IsSettled(ctx context.Context, sessionID string) (bool, error) {
	if sessionID == "" {
		return false, fmt.Errorf("broker: settled check: %w: session id must not be empty", ErrInvalid)
	}
	var settled int
	err := r.db.Reader().QueryRowContext(ctx,
		`SELECT settled FROM reconcile_state WHERE session_id = ?`, sessionID).Scan(&settled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("broker: read settle state: %w", ErrState)
	}
	return settled != 0, nil
}

// socketStopper writes session.end on a live socket and does not delete
// the provider record. Reconcile uses it when a read finds the session
// still open, so the next read can see a duration.
type socketStopper interface {
	StopSocket(ctx context.Context, providerSessionID string) error
}

// settleReadBackoff pauses between provider reads while a closing
// session still reports open. The provider writes the duration a moment
// after the close, so the first retry straight after the end usually
// reads open again. The steps total 23.5 seconds, well inside the
// process budget for one request.
var settleReadBackoff = [...]time.Duration{
	500 * time.Millisecond,
	time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	8 * time.Second,
}

// artifactReadBackoff pauses between provider reads while a closed
// session still names no artifact links. The provider attaches the
// recording and timeline links a moment after it reports the duration,
// so the first settle straight after the end usually reads neither.
// The steps total 47 seconds. Together with the close backoff above
// the waits total 70.5 seconds, inside the process budget for one
// request.
var artifactReadBackoff = [...]time.Duration{
	time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	16 * time.Second,
	16 * time.Second,
}

// readForSettle reads the provider duration. An open session is ended
// first, then read again after each backoff step until the read succeeds
// or fails with something other than an open session. When every step
// still finds the session open, the error wraps ErrSessionOpen and the
// caller settles nothing. The settle never prices a guessed duration.
// A read that carries a duration but no links waits those links out
// through waitForArtifacts. The delete waits until the artifacts are
// stored.
func (r *Reconciler) readForSettle(ctx context.Context, providerSessionID string) (ProviderSession, error) {
	read, err := r.sessions.ReadSession(ctx, providerSessionID)
	if err == nil {
		return r.waitForArtifacts(ctx, providerSessionID, read)
	}
	if !errors.Is(err, ErrSessionOpen) {
		return ProviderSession{}, fmt.Errorf("broker: reconcile: read session %s: %w", providerSessionID, err)
	}
	stop, ok := r.sessions.(socketStopper)
	if !ok {
		return ProviderSession{}, fmt.Errorf("broker: reconcile: read session %s: %w", providerSessionID, err)
	}
	if serr := stop.StopSocket(ctx, providerSessionID); serr != nil {
		return ProviderSession{}, fmt.Errorf("broker: reconcile: end live socket %s: %w", providerSessionID, serr)
	}
	for _, backoff := range settleReadBackoff {
		if werr := r.wait(ctx, backoff); werr != nil {
			return ProviderSession{}, fmt.Errorf("broker: reconcile: wait for provider close %s: %w", providerSessionID, werr)
		}
		read, err = r.sessions.ReadSession(ctx, providerSessionID)
		if err == nil {
			return r.waitForArtifacts(ctx, providerSessionID, read)
		}
		if !errors.Is(err, ErrSessionOpen) {
			return ProviderSession{}, fmt.Errorf("broker: reconcile: read session %s: %w", providerSessionID, err)
		}
	}
	return ProviderSession{}, fmt.Errorf("broker: reconcile: read session %s: %w", providerSessionID, ErrSessionOpen)
}

// waitForArtifacts keeps reading while the provider names no artifact
// links. It returns the last read whether or not both links arrived, so
// the settle always prices a known duration. A read error keeps the
// last good read instead of failing the settle, because the later sweep
// still fetches what this pass missed. The first read that carries both
// links logs how long they took to appear after the close.
func (r *Reconciler) waitForArtifacts(ctx context.Context, providerSessionID string, read ProviderSession) (ProviderSession, error) {
	start := time.Now()
	if read.RecordingURL != "" && read.TimelineURL != "" {
		log.Printf("broker: reconcile: session %s carried both artifact links after %s", providerSessionID, time.Since(start))
		return read, nil
	}
	for _, backoff := range artifactReadBackoff {
		if werr := r.wait(ctx, backoff); werr != nil {
			return read, fmt.Errorf("broker: reconcile: wait for provider artifacts %s: %w", providerSessionID, werr)
		}
		next, err := r.sessions.ReadSession(ctx, providerSessionID)
		if err != nil {
			return read, nil
		}
		read = next
		if read.RecordingURL != "" && read.TimelineURL != "" {
			log.Printf("broker: reconcile: session %s carried both artifact links after %s", providerSessionID, time.Since(start))
			return read, nil
		}
	}
	return read, nil
}

// endProvider deletes the provider record after the artifacts are stored.
// The end writes session.end before that delete when a socket is live.
// A reader that cannot end is left for the sweep, which uses the same end.
// A failed end is marked pending so a later sweep tries again. The money
// claim stays settled either way.
func (r *Reconciler) endProvider(ctx context.Context, sessionID, providerSessionID string) error {
	ender, ok := r.sessions.(SessionEnder)
	if !ok || providerSessionID == "" {
		return nil
	}
	if _, err := ender.EndSession(ctx, providerSessionID); err != nil {
		if merr := r.setEndPending(ctx, sessionID, true); merr != nil {
			return fmt.Errorf("broker: end provider session %s: %w: %w", providerSessionID, err, merr)
		}
		return fmt.Errorf("broker: end provider session %s: %w", providerSessionID, err)
	}
	if err := r.setEndPending(ctx, sessionID, false); err != nil {
		return err
	}
	return nil
}

// endPending reports whether a settled session still needs its provider
// end. The sweep retries that end and does not settle again.
func (r *Reconciler) endPending(ctx context.Context, sessionID string) (bool, error) {
	if sessionID == "" {
		return false, fmt.Errorf("broker: provider end: %w: session id must not be empty", ErrInvalid)
	}
	var pending int
	err := r.db.Writer().QueryRowContext(ctx,
		`SELECT end_pending FROM reconcile_state WHERE session_id = ?`, sessionID).Scan(&pending)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("broker: read provider end: %w", ErrState)
	}
	return pending != 0, nil
}

// setEndPending records whether the provider end still has to run.
// Pending stays set until an end succeeds, so a failed try is not forgotten.
func (r *Reconciler) setEndPending(ctx context.Context, sessionID string, pending bool) error {
	if sessionID == "" {
		return fmt.Errorf("broker: mark provider end: %w: session id must not be empty", ErrInvalid)
	}
	flag := 0
	if pending {
		flag = 1
	}
	if _, err := r.db.Writer().ExecContext(ctx,
		`UPDATE reconcile_state SET end_pending = ?, updated_at = ? WHERE session_id = ?`,
		flag, time.Now().UnixMilli(), sessionID); err != nil {
		return fmt.Errorf("broker: mark provider end: %w", ErrState)
	}
	return nil
}

// artifactsPending reports whether a settled session still waits on its
// provider artifacts. The sweep fetches them on a later pass and keeps
// the provider record until both are stored.
func (r *Reconciler) artifactsPending(ctx context.Context, sessionID string) (bool, error) {
	if sessionID == "" {
		return false, fmt.Errorf("broker: provider artifacts: %w: session id must not be empty", ErrInvalid)
	}
	var pending int
	err := r.db.Writer().QueryRowContext(ctx,
		`SELECT artifacts_pending FROM reconcile_state WHERE session_id = ?`, sessionID).Scan(&pending)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("broker: read provider artifacts: %w", ErrState)
	}
	return pending != 0, nil
}

// setArtifactsPending records whether the provider artifacts still have
// to land. The flag stays set until both artifacts are stored or the
// provider record reports gone, so a kept record is never forgotten.
func (r *Reconciler) setArtifactsPending(ctx context.Context, sessionID string, pending bool) error {
	if sessionID == "" {
		return fmt.Errorf("broker: mark provider artifacts: %w: session id must not be empty", ErrInvalid)
	}
	flag := 0
	if pending {
		flag = 1
	}
	if _, err := r.db.Writer().ExecContext(ctx,
		`UPDATE reconcile_state SET artifacts_pending = ?, updated_at = ? WHERE session_id = ?`,
		flag, time.Now().UnixMilli(), sessionID); err != nil {
		return fmt.Errorf("broker: mark provider artifacts: %w", ErrState)
	}
	return nil
}

// settleRead books one provider duration: the price at the session rate,
// settled on both ceilings under the claim, the lease closed and
// reconciled at that price, and the artifact tail after. A repeat after a
// crash settles money at most once.
func (r *Reconciler) settleRead(ctx context.Context, in Input, read ProviderSession) (Result, error) {
	price, err := priceForSeconds(read.DurationSeconds)
	if err != nil {
		return Result{}, err
	}
	overCap := read.DurationSeconds > in.TokenCapSeconds+r.margin
	won, err := r.claim(ctx, in.SessionID)
	if err != nil {
		return Result{}, err
	}
	if !won {
		return r.resumeClaimed(ctx, in)
	}
	if err := r.budgets.Settle(ctx, in.OwnerID, in.Reservation, price); err != nil {
		_ = r.unclaim(ctx, in.SessionID)
		return Result{}, fmt.Errorf("broker: reconcile: settle hold: %w: %w", ErrSettle, err)
	}
	if err := r.closeLease(ctx, in.LeaseID, price); err != nil {
		expired, ierr := r.leaseExpired(ctx, in.LeaseID)
		if ierr != nil {
			return r.review(ctx, in, fmt.Sprintf("hold settled to %s, lease %s did not close: %v", price, in.LeaseID, err))
		}
		if expired {
			return r.completeExpiredTail(ctx, in, read, price, overCap)
		}
		return r.review(ctx, in, fmt.Sprintf("hold settled to %s, lease %s did not close: %v", price, in.LeaseID, err))
	}
	if _, err := r.leases.Reconcile(ctx, in.LeaseID, price); err != nil {
		return r.review(ctx, in, fmt.Sprintf("hold settled to %s, lease %s did not reconcile: %v", price, in.LeaseID, err))
	}
	if err := r.markSettled(ctx, in.SessionID, read.DurationSeconds, price, overCap, read.RecordingURL, read.TimelineURL); err != nil {
		return r.review(ctx, in, fmt.Sprintf("hold settled to %s, state write failed: %v", price, err))
	}
	row, err := r.readRow(ctx, in.SessionID)
	if err != nil {
		return Result{}, err
	}
	return r.finishArtifacts(ctx, in, row)
}

// resumeClaimed continues a session another attempt claimed. The lease
// state decides. A reconciled lease proves the money path ran, because
// the reconcile call closes it, so the resume repairs the metadata from
// a fresh provider read and finishes the artifacts. A closed lease proves
// the budget settle ran, because the close runs after it, so the resume
// finishes the reconcile and then the same tail. Any other state stops
// for review without moving money. An expired lease proves nothing about
// the settle, because the close never ran, and a second settle would book
// spend twice, so expiry on this path reviews. Only the run that settled
// under its own claim may complete an expired tail.
func (r *Reconciler) resumeClaimed(ctx context.Context, in Input) (Result, error) {
	found, err := r.leases.Inspect(ctx, in.LeaseID)
	if err != nil {
		return r.review(ctx, in, fmt.Sprintf("settle state is unknown, lease %s is unreadable: %v", in.LeaseID, err))
	}
	switch {
	case found.Reconciled:
	case found.State == lease.StateClosed:
		if _, err := r.leases.Reconcile(ctx, in.LeaseID, found.Settled); err != nil {
			return r.review(ctx, in, fmt.Sprintf("lease %s did not reconcile: %v", in.LeaseID, err))
		}
	default:
		return r.review(ctx, in, fmt.Sprintf("settle state is unknown, lease %s reads %s", in.LeaseID, found.State))
	}
	read, err := r.sessions.ReadSession(ctx, in.ProviderSessionID)
	if err != nil {
		return r.review(ctx, in, fmt.Sprintf("lease %s stands settled, provider read failed: %v", in.LeaseID, err))
	}
	price, err := priceForSeconds(read.DurationSeconds)
	if err != nil {
		return Result{}, err
	}
	overCap := read.DurationSeconds > in.TokenCapSeconds+r.margin
	if err := r.markSettled(ctx, in.SessionID, read.DurationSeconds, price, overCap, read.RecordingURL, read.TimelineURL); err != nil {
		return Result{}, err
	}
	row, err := r.readRow(ctx, in.SessionID)
	if err != nil {
		return Result{}, err
	}
	return r.finishArtifacts(ctx, in, row)
}

// closeLease ends the lease at the provider price. A lease that already
// reads closed needs no second close, because the claim guard proves this
// process closed it.
func (r *Reconciler) closeLease(ctx context.Context, leaseID string, price cost.Price) error {
	if _, err := r.leases.Close(ctx, leaseID, price); err != nil {
		found, ierr := r.leases.Inspect(ctx, leaseID)
		if ierr != nil {
			return fmt.Errorf("broker: close lease: %w: %w", ErrSettle, err)
		}
		if found.State != lease.StateClosed {
			return fmt.Errorf("broker: close lease: %w: %w", ErrSettle, err)
		}
	}
	return nil
}

// leaseExpired reports whether the lease reads expired. Expiry is terminal,
// because the cap passed before the close and no later close can land. The
// caller runs only after this run settled the hold under its claim. A
// resume must never use this answer to complete, because it cannot prove
// the settle ran and a second settle would book spend twice.
func (r *Reconciler) leaseExpired(ctx context.Context, leaseID string) (bool, error) {
	found, err := r.leases.Inspect(ctx, leaseID)
	if err != nil {
		return false, fmt.Errorf("broker: inspect lease: %w: %w", ErrSettle, err)
	}
	return found.State == lease.StateExpired, nil
}

// completeExpiredTail finishes a run whose hold settled and whose lease
// expired before the close. Money moved exactly once under this run's
// claim, the provider numbers are known, and the lease can never close, so
// the run writes the duration, persists the artifacts, and marks the claim
// settled instead of asking for review. The lease row stays expired and
// unreconciled, because expiry accepts no further move. The money truth
// lives in the budget and the claim row.
func (r *Reconciler) completeExpiredTail(ctx context.Context, in Input, read ProviderSession, price cost.Price, overCap bool) (Result, error) {
	if err := r.markSettled(ctx, in.SessionID, read.DurationSeconds, price, overCap, read.RecordingURL, read.TimelineURL); err != nil {
		return r.review(ctx, in, fmt.Sprintf("hold settled to %s, state write failed: %v", price, err))
	}
	row, err := r.readRow(ctx, in.SessionID)
	if err != nil {
		return Result{}, err
	}
	return r.finishArtifacts(ctx, in, row)
}

// finishArtifacts completes the idempotent tail: the session row update,
// the recording and timeline persists, and the over cap alert. A claimed
// media id stays, so a repeat fetches nothing and stores nothing. The
// alert fires once per session on its durable flag. The provider record
// ends only once both media ids are stored. Otherwise the flag marks the
// artifacts pending, the money stays settled, and the record is kept for
// the sweep to fetch later. Money never moves here.
func (r *Reconciler) finishArtifacts(ctx context.Context, in Input, row claimRow) (Result, error) {
	res := Result{
		SessionID:        in.SessionID,
		ConnectedSeconds: row.connectedSeconds,
		TimelineURL:      row.timelineURL,
		RecordingMediaID: row.recordingMediaID,
		TimelineMediaID:  row.timelineMediaID,
	}
	price, err := priceForSeconds(row.connectedSeconds)
	if err != nil {
		return Result{}, err
	}
	res.Cost = price
	res.OverCap = row.overCap
	if err := r.diary.SetConnectedSeconds(ctx, in.SessionID, row.connectedSeconds); err != nil {
		return res, fmt.Errorf("broker: write session duration: %w: %w", ErrState, err)
	}
	if row.recordingURL != "" {
		mediaID, err := r.ensureArtifact(ctx, in, "recording", row.recordingURL, r.contentType, row.recordingMediaID)
		if err != nil {
			return res, err
		}
		res.RecordingMediaID = mediaID
	}
	if row.timelineURL != "" {
		mediaID, err := r.ensureArtifact(ctx, in, "timeline", row.timelineURL, r.timelineType, row.timelineMediaID)
		if err != nil {
			return res, err
		}
		res.TimelineMediaID = mediaID
	}
	if res.OverCap && !row.overCapAlerted {
		r.alert(ctx, &res, AlertOverCap, in.OwnerID,
			fmt.Sprintf("session ran %d seconds past a %d second cap", res.ConnectedSeconds, in.TokenCapSeconds))
		if res.AlertError == "" {
			if err := r.markOverCapAlerted(ctx, in.SessionID); err != nil {
				return res, err
			}
		}
	}
	if res.RecordingMediaID != "" && res.TimelineMediaID != "" {
		if err := r.setArtifactsPending(ctx, in.SessionID, false); err != nil {
			return res, err
		}
		if err := r.endProvider(ctx, in.SessionID, in.ProviderSessionID); err != nil {
			return res, err
		}
		return res, nil
	}
	if err := r.setArtifactsPending(ctx, in.SessionID, true); err != nil {
		return res, err
	}
	return res, nil
}

// artifactFlights records artifact fetches running in this process.
// Two reconciler values do not share a guard. A claimed id can still
// be mid fetch here. A new process has an empty set, so it can finish
// a claim whose blob never landed.
type artifactFlights struct {
	mu sync.Mutex
	n  map[string]int
}

// artifactFlight is process wide on purpose. The per value guard does
// not cover a second Reconciler on the same database.
var artifactFlight artifactFlights

func artifactKey(sessionID, kind string) string {
	return sessionID + "/" + kind
}

// enter counts one fetch that is about to claim or already owns the id.
func (f *artifactFlights) enter(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.n == nil {
		f.n = map[string]int{}
	}
	f.n[key]++
}

// leave drops one fetch. The key goes when the last fetch leaves.
func (f *artifactFlights) leave(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n[key]--
	if f.n[key] <= 0 {
		delete(f.n, key)
	}
}

// tryStart begins a fetch only when none is running for key.
// False means a peer in this process already holds the fetch.
func (f *artifactFlights) tryStart(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.n[key] > 0 {
		return false
	}
	if f.n == nil {
		f.n = map[string]int{}
	}
	f.n[key] = 1
	return true
}

// ensureArtifact stores one artifact once. An id already on the row is
// kept. Otherwise this pass claims a fresh id before the fetch, then
// stores the bytes under that id. A peer that loses the claim does not
// fetch.
func (r *Reconciler) ensureArtifact(ctx context.Context, in Input, kind, artifactURL, contentType, knownID string) (string, error) {
	if knownID != "" {
		return r.finishClaimed(ctx, in, kind, artifactURL, contentType, knownID)
	}
	mediaID, err := id.New()
	if err != nil {
		return "", fmt.Errorf("broker: persist %s: %w: %w", kind, ErrState, err)
	}
	key := artifactKey(in.SessionID, kind)
	artifactFlight.enter(key)
	defer artifactFlight.leave(key)
	won, err := r.claimArtifact(ctx, in.SessionID, kind, mediaID)
	if err != nil {
		return "", err
	}
	if !won {
		return r.claimedArtifactID(ctx, in.SessionID, kind)
	}
	if err := r.fetchAndPersist(ctx, in, kind, artifactURL, contentType, mediaID); err != nil {
		return "", err
	}
	return mediaID, nil
}

// finishClaimed returns mediaID when its blob is already stored or a
// fetch for it is running in this process. Otherwise the earlier pass
// died before the row landed, and this pass stores the same id.
func (r *Reconciler) finishClaimed(ctx context.Context, in Input, kind, artifactURL, contentType, mediaID string) (string, error) {
	stored, err := r.blobStored(ctx, mediaID)
	if err != nil {
		return "", err
	}
	if stored {
		return mediaID, nil
	}
	key := artifactKey(in.SessionID, kind)
	if !artifactFlight.tryStart(key) {
		return mediaID, nil
	}
	defer artifactFlight.leave(key)
	stored, err = r.blobStored(ctx, mediaID)
	if err != nil {
		return "", err
	}
	if stored {
		return mediaID, nil
	}
	if err := r.fetchAndPersist(ctx, in, kind, artifactURL, contentType, mediaID); err != nil {
		return "", err
	}
	return mediaID, nil
}

// claimedArtifactID returns the id a peer wrote. An empty id after a
// lost claim is a broken row, not a cue to fetch again.
func (r *Reconciler) claimedArtifactID(ctx context.Context, sessionID, kind string) (string, error) {
	mediaID, err := r.artifactID(ctx, sessionID, kind)
	if err != nil {
		return "", err
	}
	if mediaID == "" {
		return "", fmt.Errorf("broker: persist %s: %w: the claim lost and the id is empty", kind, ErrState)
	}
	return mediaID, nil
}

// blobStored reports whether the media index has a row for mediaID.
// The writer connection is the one the store just committed on.
func (r *Reconciler) blobStored(ctx context.Context, mediaID string) (bool, error) {
	if mediaID == "" {
		return false, nil
	}
	var n int
	if err := r.db.Writer().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media WHERE id = ?`, mediaID).Scan(&n); err != nil {
		return false, fmt.Errorf("broker: read media %s: %w: %w", mediaID, ErrState, err)
	}
	return n > 0, nil
}

// fetchAndPersist downloads one artifact and stores it under mediaID.
// The id is already on the claim row, so a second insert of the same
// URL cannot commit. A file left at that id without a row is indexed
// when it matches this download, and replaced when it does not.
func (r *Reconciler) fetchAndPersist(ctx context.Context, in Input, kind, artifactURL, contentType, mediaID string) error {
	body, err := r.artifacts.Fetch(ctx, artifactURL)
	if err != nil {
		return fmt.Errorf("broker: fetch %s: %w: %w", kind, ErrRead, err)
	}
	defer body.Close()
	writer, ok := r.media.(idMediaWriter)
	if !ok {
		return fmt.Errorf("broker: persist %s: %w: the media writer cannot store a reserved id", kind, ErrState)
	}
	capped := &cappedReader{inner: body, left: r.maxBytes + 1}
	put := mediastore.Put{
		ContentType: contentType,
		Owner:       in.OwnerID,
		Group:       in.EpisodeID,
		Visibility:  mediastore.Private,
	}
	err = writer.PersistWithID(ctx, mediaID, capped, put)
	if err == nil {
		return nil
	}
	if errors.Is(err, errArtifactTooLarge) {
		return fmt.Errorf("broker: fetch %s: %w: artifact passes the byte cap", kind, ErrRead)
	}
	if errors.Is(err, mediastore.ErrAlreadyExists) {
		return r.recoverClaimedFile(ctx, kind, mediaID, capped, put)
	}
	return fmt.Errorf("broker: persist %s: %w: %w", kind, ErrState, err)
}

// recoverClaimedFile stores mediaID when its file is already on disk and
// its row is not. A file that matches the download is indexed in place.
// A different file is a short write, so it is removed and the download
// is stored under the same id. Indexing the short file would publish a
// truncated recording.
//
// The store creates the file before it copies bytes, and an existing
// name fails before that copy. The stream is still the full download.
func (r *Reconciler) recoverClaimedFile(ctx context.Context, kind, mediaID string, src io.Reader, put mediastore.Put) error {
	stored, err := r.blobStored(ctx, mediaID)
	if err != nil {
		return err
	}
	if stored {
		return nil
	}
	raw, err := io.ReadAll(src)
	if err != nil {
		if errors.Is(err, errArtifactTooLarge) {
			return fmt.Errorf("broker: fetch %s: %w: artifact passes the byte cap", kind, ErrRead)
		}
		return fmt.Errorf("broker: fetch %s: %w: %w", kind, ErrRead, err)
	}
	stored, err = r.blobStored(ctx, mediaID)
	if err != nil {
		return err
	}
	if stored {
		return nil
	}
	dir, ok := mediaDirectory(r.media)
	if !ok || !id.Valid(mediaID) {
		return fmt.Errorf("broker: persist %s: %w: the claimed file is already on disk", kind, ErrState)
	}
	path := filepath.Join(dir, mediaID)
	onDisk, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return r.persistClaimedID(ctx, kind, mediaID, raw, put)
		}
		return fmt.Errorf("broker: persist %s: %w: %w", kind, ErrState, err)
	}
	if bytes.Equal(onDisk, raw) {
		return r.indexClaimedFile(ctx, kind, mediaID, int64(len(raw)), put)
	}
	stored, err = r.blobStored(ctx, mediaID)
	if err != nil {
		return err
	}
	if stored {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("broker: persist %s: %w: %w", kind, ErrState, err)
	}
	return r.persistClaimedID(ctx, kind, mediaID, raw, put)
}

// persistClaimedID stores raw under mediaID. The caller already removed
// any short file at that name, or the name was free.
func (r *Reconciler) persistClaimedID(ctx context.Context, kind, mediaID string, raw []byte, put mediastore.Put) error {
	writer, ok := r.media.(idMediaWriter)
	if !ok {
		return fmt.Errorf("broker: persist %s: %w: the media writer cannot store a reserved id", kind, ErrState)
	}
	if err := writer.PersistWithID(ctx, mediaID, bytes.NewReader(raw), put); err != nil {
		return fmt.Errorf("broker: persist %s: %w: %w", kind, ErrState, err)
	}
	return nil
}

// indexClaimedFile inserts the media row for a file that is already
// durable. The row is what makes the file reachable. A conflicting row
// means another pass stored it.
func (r *Reconciler) indexClaimedFile(ctx context.Context, kind, mediaID string, size int64, put mediastore.Put) error {
	contentType, err := bareContentType(put.ContentType)
	if err != nil {
		return fmt.Errorf("broker: persist %s: %w: %w", kind, ErrState, err)
	}
	visibility := "private"
	if put.Visibility == mediastore.Public {
		visibility = string(mediastore.Public)
	}
	res, err := r.db.Writer().ExecContext(ctx,
		`INSERT INTO media (id, owner, media_group, content_type, size_bytes, visibility, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO NOTHING`,
		mediaID, put.Owner, put.Group, contentType, size, visibility, time.Now().UTC().UnixNano())
	if err != nil {
		return fmt.Errorf("broker: persist %s: %w: %w", kind, ErrState, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("broker: persist %s: %w: %w", kind, ErrState, err)
	}
	if n == 1 {
		return nil
	}
	stored, err := r.blobStored(ctx, mediaID)
	if err != nil {
		return err
	}
	if stored {
		return nil
	}
	return fmt.Errorf("broker: persist %s: %w: the media row did not land", kind, ErrState)
}

// mediaDirectory reads the directory the media store writes into.
// DeleteIfPresent leaves a file in place when its row is missing, and
// the store does not expose that directory. Recovery needs the path to
// tell a short file from a finished download.
func mediaDirectory(media MediaWriter) (string, bool) {
	store, ok := media.(*mediastore.Store)
	if !ok {
		return "", false
	}
	field := reflect.ValueOf(store).Elem().FieldByName("dir")
	if !field.IsValid() || field.Kind() != reflect.String {
		return "", false
	}
	dir := field.String()
	if dir == "" {
		return "", false
	}
	return dir, true
}

// bareContentType cuts parameters and case the way the media store does,
// so a recovered row is served as the same type the store would write.
func bareContentType(contentType string) (string, error) {
	bare, _, _ := strings.Cut(contentType, ";")
	bare = strings.ToLower(strings.TrimSpace(bare))
	if bare == "" {
		return "", fmt.Errorf("content type is empty")
	}
	return bare, nil
}

// cappedReader counts a stream and fails past the cap. The media persist
// reads through it, so an oversize artifact aborts the write and the store
// removes the partial file it owns.
type cappedReader struct {
	inner io.ReadCloser
	left  int64
}

// errArtifactTooLarge reports an artifact past the byte cap.
var errArtifactTooLarge = errors.New("broker: artifact passes the byte cap")

// Read pulls the next bytes and counts them. Past the cap it reports the
// artifact too large, which aborts the persist reading it.
func (c *cappedReader) Read(b []byte) (int, error) {
	if c.left <= 0 {
		return 0, errArtifactTooLarge
	}
	if int64(len(b)) > c.left {
		b = b[:c.left]
	}
	n, err := c.inner.Read(b)
	c.left -= int64(n)
	return n, err
}

// Close closes the wrapped stream.
func (c *cappedReader) Close() error {
	return c.inner.Close()
}

// HTTPArtifactFetcher downloads artifact URLs over plain HTTPS. Artifact
// URLs carry their own grant, so no key travels with the request.
type HTTPArtifactFetcher struct {
	client *http.Client
}

// NewHTTPArtifactFetcher returns a fetcher over client. A nil client
// becomes a 30 second client, so no fetch blocks past the process budget
// for one request.
func NewHTTPArtifactFetcher(client *http.Client) *HTTPArtifactFetcher {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &HTTPArtifactFetcher{client: client}
}

// Fetch returns the artifact bytes as a stream for a full URL. Only a 200
// reply counts. The caller closes the stream.
func (f *HTTPArtifactFetcher) Fetch(ctx context.Context, artifactURL string) (io.ReadCloser, error) {
	if artifactURL == "" {
		return nil, fmt.Errorf("broker: fetch artifact: %w: URL must not be empty", ErrInvalid)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, artifactURL, nil)
	if err != nil {
		return nil, fmt.Errorf("broker: fetch artifact: %w: %w", ErrRead, err)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("broker: fetch artifact call: %w: %w", ErrRead, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("broker: fetch artifact status %d: %w", resp.StatusCode, ErrRead)
	}
	return resp.Body, nil
}
