// Package broker starts live sessions behind seven ordered checks.
//
// This file settles them. After a session ends, reconciliation reads the
// real connected seconds from the provider, settles the mint time budget
// hold to the real cost, closes the lease against the provider number, and
// persists the stereo recording privately, because artifact URLs expire.
// The provider read repeats safely, so the job kind is idempotent. The
// money settle runs exactly once, guarded by a durable claim row this file
// owns. An ambiguous resume never settles twice. It stops for review.
package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/nrynss/keel/cost"
	costsqlitestore "github.com/nrynss/keel/cost/sqlitestore"
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

// DefaultFetchMaxBytes caps one artifact download. A three hour stereo
// recording fits with room, and anything larger is a provider surprise
// worth refusing rather than streaming to disk unbounded.
const DefaultFetchMaxBytes = 512 << 20

// Sentinels, one per failure condition.
var (
	// ErrRead reports a provider session read or artifact fetch that
	// failed. Reads repeat safely, so the job retries them.
	ErrRead = errors.New("broker: read provider session")
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
// recording, TimelineURL the timeline. Both expire, so the recording lands
// in the media store on receipt and the URLs stay as metadata.
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
type MediaWriter interface {
	// Persist writes src as a new blob and returns its id.
	Persist(ctx context.Context, src io.Reader, blob mediastore.Put) (string, error)
}

// AlertKind names why the reconciler raised an alert.
type AlertKind string

// Alert kinds. OverCap should never fire. NeedsReview fires when an
// ambiguous resume leaves money unsettled for an operator.
const (
	// AlertOverCap marks a session connected past its cap plus margin.
	AlertOverCap AlertKind = "over_cap"
	// AlertNeedsReview marks a session whose resume could not prove the
	// settle state, so it settled nothing.
	AlertNeedsReview AlertKind = "needs_review"
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
	_ Settler      = (*costsqlitestore.KeyedBudget)(nil)
	_ LeaseSettler = (*lease.Manager)(nil)
	_ MediaWriter  = (*mediastore.Store)(nil)
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
	// Media persists the stereo recording. It must not be nil.
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
	// FetchMaxBytes caps one artifact download. Zero or negative means
	// the default.
	FetchMaxBytes int64
}

// Reconciler settles live sessions against provider truth. Create it with
// New, because the zero value holds no stores. A Reconciler is safe for
// concurrent use, and one session settles through one job at a time.
type Reconciler struct {
	db          *sqlite.DB
	sessions    SessionReader
	artifacts   ArtifactFetcher
	budgets     Settler
	leases      LeaseSettler
	diary       SessionStore
	media       MediaWriter
	alerter     Alerter
	margin      int
	contentType string
	maxBytes    int64
}

// claimRow is one durable settle claim.
type claimRow struct {
	claimed          bool
	settled          bool
	connectedSeconds int
	overCap          bool
	overCapAlerted   bool
	recordingMediaID string
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
	maxBytes := cfg.FetchMaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultFetchMaxBytes
	}
	r := &Reconciler{
		db:          cfg.DB,
		sessions:    cfg.Sessions,
		artifacts:   cfg.Artifacts,
		budgets:     cfg.Budgets,
		leases:      cfg.Leases,
		diary:       cfg.Diary,
		media:       cfg.Media,
		alerter:     cfg.Alerter,
		margin:      cfg.MarginSeconds,
		contentType: contentType,
		maxBytes:    maxBytes,
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
		recording_url TEXT NOT NULL DEFAULT '',
		timeline_url TEXT NOT NULL DEFAULT '',
		updated_at INTEGER NOT NULL DEFAULT 0
	)`
	if _, err := r.db.Writer().ExecContext(context.Background(), schema); err != nil {
		return nil, fmt.Errorf("broker: create reconcile state: %w", ErrState)
	}
	if err := r.ensureAlertColumn(context.Background()); err != nil {
		return nil, err
	}
	return r, nil
}

// ensureAlertColumn adds the over cap alert flag to claim tables written
// before the flag existed. Fresh tables already carry it from the schema,
// so the common path changes nothing.
func (r *Reconciler) ensureAlertColumn(ctx context.Context) error {
	rows, err := r.db.Reader().QueryContext(ctx, `PRAGMA table_info(reconcile_state)`)
	if err != nil {
		return fmt.Errorf("broker: read reconcile columns: %w", ErrState)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, ctype string
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return fmt.Errorf("broker: read reconcile columns: %w", ErrState)
		}
		if name == "over_cap_alerted" {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("broker: read reconcile columns: %w", ErrState)
	}
	if _, err := r.db.Writer().ExecContext(ctx,
		`ALTER TABLE reconcile_state ADD COLUMN over_cap_alerted INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("broker: add reconcile alert column: %w", ErrState)
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

// validate rejects an input the reconciler cannot settle.
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
	var mediaID, recordingURL, timelineURL string
	err := r.db.Reader().QueryRowContext(ctx,
		`SELECT claimed, settled, connected_seconds, over_cap, over_cap_alerted, recording_media_id, recording_url, timeline_url
		FROM reconcile_state WHERE session_id = ?`, sessionID).Scan(
		&claimed, &settled, &seconds, &overCap, &overCapAlerted, &mediaID, &recordingURL, &timelineURL)
	if err != nil {
		return claimRow{}, fmt.Errorf("broker: read reconcile row: %w", ErrState)
	}
	row.claimed = claimed != 0
	row.settled = settled != 0
	row.connectedSeconds = seconds
	row.overCap = overCap != 0
	row.overCapAlerted = overCapAlerted != 0
	row.recordingMediaID = mediaID
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

// markRecording records the persisted recording id.
func (r *Reconciler) markRecording(ctx context.Context, sessionID, mediaID string) error {
	if _, err := r.db.Writer().ExecContext(ctx,
		`UPDATE reconcile_state SET recording_media_id = ?, updated_at = ? WHERE session_id = ?`,
		mediaID, time.Now().UnixMilli(), sessionID); err != nil {
		return fmt.Errorf("broker: mark reconcile recording: %w", ErrState)
	}
	return nil
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
// provider duration, settles the mint time hold to the real cost on both
// ceilings, closes the lease at the provider number, writes the duration
// on the session row, and persists the stereo recording privately. A
// repeat after a crash settles money at most once. Artifact retries skip
// settled money and finish the bytes.
func (r *Reconciler) Reconcile(ctx context.Context, in Input) (Result, error) {
	if err := validate(in); err != nil {
		return Result{}, err
	}
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
	read, err := r.sessions.ReadSession(ctx, in.ProviderSessionID)
	if err != nil {
		return Result{}, fmt.Errorf("broker: reconcile: read session %s: %w", in.ProviderSessionID, err)
	}
	if read.DurationSeconds < 0 {
		return Result{}, fmt.Errorf("broker: reconcile: %w: provider duration is negative", ErrRead)
	}
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
	row, err = r.readRow(ctx, in.SessionID)
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
// the recording persist, and the over cap alert. The alert fires once per
// session on its durable flag, so retries stay silent. Money never moves
// here, so retries finish the bytes without spending twice.
func (r *Reconciler) finishArtifacts(ctx context.Context, in Input, row claimRow) (Result, error) {
	res := Result{
		SessionID:        in.SessionID,
		ConnectedSeconds: row.connectedSeconds,
		TimelineURL:      row.timelineURL,
		RecordingMediaID: row.recordingMediaID,
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
	if row.recordingURL != "" && row.recordingMediaID == "" {
		mediaID, err := r.persistRecording(ctx, in, row.recordingURL)
		if err != nil {
			return res, err
		}
		res.RecordingMediaID = mediaID
	}
	if res.OverCap && !row.overCapAlerted {
		r.alert(ctx, &res, AlertOverCap, in.OwnerID,
			fmt.Sprintf("session ran %d seconds past a %d second cap", res.ConnectedSeconds, in.TokenCapSeconds))
		if res.AlertError != "" {
			return res, nil
		}
		if err := r.markOverCapAlerted(ctx, in.SessionID); err != nil {
			return res, err
		}
	}
	return res, nil
}

// persistRecording fetches the stereo recording and stores it privately.
// Artifact URLs expire, so the bytes land in the media store on receipt.
func (r *Reconciler) persistRecording(ctx context.Context, in Input, recordingURL string) (string, error) {
	body, err := r.artifacts.Fetch(ctx, recordingURL)
	if err != nil {
		return "", fmt.Errorf("broker: fetch recording: %w: %w", ErrRead, err)
	}
	defer body.Close()
	capped := &cappedReader{inner: body, left: r.maxBytes + 1}
	mediaID, err := r.media.Persist(ctx, capped, mediastore.Put{
		ContentType: r.contentType,
		Owner:       in.OwnerID,
		Group:       in.EpisodeID,
		Visibility:  mediastore.Private,
	})
	if err != nil {
		if errors.Is(err, errArtifactTooLarge) {
			return "", fmt.Errorf("broker: fetch recording: %w: artifact passes the byte cap", ErrRead)
		}
		return "", fmt.Errorf("broker: persist recording: %w: %w", ErrState, err)
	}
	if err := r.markRecording(ctx, in.SessionID, mediaID); err != nil {
		return "", err
	}
	return mediaID, nil
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
