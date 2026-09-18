// Abandoned sessions end here. No server call ends a live session: the
// provider record deletes while the socket stays open and billable. The
// sweep therefore settles first and deletes after. It reads every session
// row still open past its cap plus a margin, books the accrued cost on
// both ceilings through the reconciler, removes the provider record, and
// records the outcome on its own row. The kind is idempotent, because
// ending an ended session stays safe. A second pass over a swept session
// moves no money and raises no alert.
package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nrynss/keel/cost"
	costsqlitestore "github.com/nrynss/keel/cost/sqlitestore"
	"github.com/nrynss/keel/job"
	"github.com/nrynss/keel/sqlite"
)

// SweepKindName is the job kind the sweep runs under. The wiring registers
// it with Kind, so a restart resumes an unfinished sweep instead of
// dropping it.
const SweepKindName = "sweep"

// ErrSweep reports a sweep that cannot run: a bad candidate, an unreadable
// source, or a durable write the sweep owns.
var ErrSweep = errors.New("broker: sweep sessions")

// ErrProviderGone reports a provider session the status read no longer
// finds. The sweep settles nothing for it and asks for review, because a
// settle prices nothing without a duration.
var ErrProviderGone = errors.New("broker: provider session is gone")

// Candidate is one session row the source believes is still open. The
// source carries the mint time linkage, because no diary column links a
// lease row to a session row yet. OpenSeconds is how long the row has
// been open, measured by the source.
type Candidate struct {
	// SessionID is the diary session row.
	SessionID string `json:"session_id"`
	// OwnerID is the session owner.
	OwnerID string `json:"owner_id"`
	// EpisodeID is the episode the session recorded.
	EpisodeID string `json:"episode_id"`
	// ProviderSessionID is the provider session to end and settle.
	ProviderSessionID string `json:"provider_session_id"`
	// LeaseID is the lease the mint opened.
	LeaseID string `json:"lease_id"`
	// Reservation is the mint time budget hold to settle.
	Reservation costsqlitestore.Reservation `json:"reservation"`
	// TokenCapSeconds is the session cap the mint reserved for.
	TokenCapSeconds int `json:"token_cap_seconds"`
	// OpenSeconds is how long the row has been open.
	OpenSeconds int `json:"open_seconds"`
}

// ProviderStatus is what the provider reports for one session. HasDuration
// stays false while the session runs, because open sessions report no
// duration. OpenSeconds is the elapsed time since the provider opened the
// session. RecordingURL and TimelineURL ride along when the provider still
// names them. Open sessions carry none.
type ProviderStatus struct {
	// ID is the provider session id that was read.
	ID string `json:"id"`
	// Status is the provider lifecycle state.
	Status string `json:"status"`
	// CloseReason is why the session closed, or empty while open.
	CloseReason string `json:"close_reason"`
	// HasDuration reports a usable duration is present.
	HasDuration bool `json:"has_duration"`
	// DurationSeconds is the connected time in seconds when present.
	DurationSeconds int `json:"duration_seconds"`
	// OpenSeconds is the elapsed time since the provider opened it.
	OpenSeconds int `json:"open_seconds"`
	// RecordingURL is the expiring stereo recording URL, or empty.
	RecordingURL string `json:"recording_url"`
	// TimelineURL is the expiring timeline URL, or empty.
	TimelineURL string `json:"timeline_url"`
}

// EndResult is one provider record delete. Deleted reports the provider
// removed the record on this call. False means the record was already
// gone, which still counts as success.
type EndResult struct {
	// Deleted reports the record was removed on this call.
	Deleted bool `json:"deleted"`
	// Detail says what the delete did in plain words.
	Detail string `json:"detail"`
}

// SweepSource lists the session rows the sweep should inspect. The diary
// implements it once the lease and reservation link lands on the session
// rows. Until then the wiring passes a source that carries the linkage.
type SweepSource interface {
	// ListOpen returns the session rows believed still open.
	ListOpen(ctx context.Context) ([]Candidate, error)
}

// StatusReader reads one provider session status. The provider package
// implements it over GET on its sessions endpoint. The sweep declares the
// seam, so this file never carries the provider host or key.
type StatusReader interface {
	// ReadStatus returns the provider status for a provider session id.
	// A record the provider no longer keeps reports ErrProviderGone.
	ReadStatus(ctx context.Context, providerSessionID string) (ProviderStatus, error)
}

// SessionEnder deletes one provider session record. Ending an ended
// session succeeds, so a repeated sweep never fails on a swept session.
type SessionEnder interface {
	// EndSession deletes the provider record for a provider session id.
	EndSession(ctx context.Context, providerSessionID string) (EndResult, error)
}

// SweepOutcome is what one sweep did to one session.
type SweepOutcome struct {
	// SessionID is the diary session that was swept.
	SessionID string `json:"session_id"`
	// ProviderStatus is what the provider reported before the settle.
	ProviderStatus string `json:"provider_status"`
	// ConnectedSeconds is the accrued cost the settle booked.
	ConnectedSeconds int `json:"connected_seconds"`
	// Cost is the booked price at the session rate.
	Cost cost.Price `json:"cost_nd"`
	// ServerEnded reports a server call ended the session. No server call
	// does today, so this stays false and the detail says so.
	ServerEnded bool `json:"server_ended"`
	// Skipped reports the sweep left the session alone, with the reason
	// in Detail.
	Skipped bool `json:"skipped"`
	// Detail carries the outcome in plain words.
	Detail string `json:"detail"`
}

// SweeperConfig carries everything the sweep needs. The zero value is not
// usable.
type SweeperConfig struct {
	// DB holds the sweep rows beside every other store. It must not be nil.
	DB *sqlite.DB
	// Source lists the open session rows. It must not be nil.
	Source SweepSource
	// Statuses reads provider session statuses. It must not be nil.
	Statuses StatusReader
	// Ender deletes provider session records. It must not be nil.
	Ender SessionEnder
	// Reconciler settles the mint time holds. It must not be nil.
	Reconciler *Reconciler
	// Alerter delivers sweep alerts. Nil records them on the outcome only.
	Alerter Alerter
	// MarginSeconds is the grace past the cap before a session counts as
	// abandoned. Zero flags past the cap exactly. Negative is invalid.
	MarginSeconds int
	// Now reports the current time. Nil means the real clock. Tests pass
	// a fixed clock, so no sweep test sleeps.
	Now func() time.Time
}

// Sweeper ends abandoned sessions and settles their money. Create it with
// NewSweeper, because the zero value holds no stores. A Sweeper is safe
// for concurrent use, and one session sweeps through one job at a time.
type Sweeper struct {
	db         *sqlite.DB
	source     SweepSource
	statuses   StatusReader
	ender      SessionEnder
	reconciler *Reconciler
	alerter    Alerter
	margin     int
	now        func() time.Time
}

// NewSweeper validates cfg, creates the sweep table, and returns the
// sweeper.
func NewSweeper(cfg SweeperConfig) (*Sweeper, error) {
	if cfg.DB == nil {
		return nil, fmt.Errorf("broker: new sweeper: %w: database must not be nil", ErrSweep)
	}
	if cfg.Source == nil {
		return nil, fmt.Errorf("broker: new sweeper: %w: source must not be nil", ErrSweep)
	}
	if cfg.Statuses == nil {
		return nil, fmt.Errorf("broker: new sweeper: %w: status reader must not be nil", ErrSweep)
	}
	if cfg.Ender == nil {
		return nil, fmt.Errorf("broker: new sweeper: %w: session ender must not be nil", ErrSweep)
	}
	if cfg.Reconciler == nil {
		return nil, fmt.Errorf("broker: new sweeper: %w: reconciler must not be nil", ErrSweep)
	}
	if cfg.MarginSeconds < 0 {
		return nil, fmt.Errorf("broker: new sweeper: %w: margin must not be negative", ErrSweep)
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	s := &Sweeper{
		db:         cfg.DB,
		source:     cfg.Source,
		statuses:   cfg.Statuses,
		ender:      cfg.Ender,
		reconciler: cfg.Reconciler,
		alerter:    cfg.Alerter,
		margin:     cfg.MarginSeconds,
		now:        now,
	}
	const schema = `CREATE TABLE IF NOT EXISTS sweep_state (
		session_id TEXT PRIMARY KEY,
		provider_status TEXT NOT NULL DEFAULT '',
		connected_seconds INTEGER NOT NULL DEFAULT 0,
		server_ended INTEGER NOT NULL DEFAULT 0,
		detail TEXT NOT NULL DEFAULT '',
		updated_at INTEGER NOT NULL DEFAULT 0
	)`
	if _, err := s.db.Writer().ExecContext(context.Background(), schema); err != nil {
		return nil, fmt.Errorf("broker: create sweep state: %w", ErrSweep)
	}
	return s, nil
}

// Kind returns the job kind the sweep runs under. Ending repeats safely,
// and settling runs at most once through the reconciler, so the kind is
// idempotent with three attempts, and Resume rebuilds the run from the
// interrupted record.
func (s *Sweeper) Kind() job.Kind {
	return job.Kind{
		Idempotent:  true,
		MaxAttempts: 3,
		Resume:      s.Resume,
	}
}

// SweepInput is one sweep run. The margin rides on the progress snapshot,
// so a resume rebuilds the run from the record alone. Candidates are
// listed fresh on every run, because rows open and close between runs.
type SweepInput struct {
	// MarginSeconds is the grace past the cap before a session counts as
	// abandoned. Negative means the sweeper default.
	MarginSeconds int `json:"margin_seconds"`
}

// Resume rebuilds the work for an interrupted sweep record.
func (s *Sweeper) Resume(rec job.Record) (job.Func, error) {
	if len(rec.Progress.Detail) == 0 {
		return nil, fmt.Errorf("broker: resume sweep: %w: record carries no input", ErrInvalid)
	}
	var in SweepInput
	if err := json.Unmarshal(rec.Progress.Detail, &in); err != nil {
		return nil, fmt.Errorf("broker: resume sweep: %w: record input is corrupt", ErrInvalid)
	}
	return s.runFunc(in), nil
}

// runFunc wraps one sweep as job work. It reports the input before the
// source listing, so any crash after that point resumes with the margin
// intact.
func (s *Sweeper) runFunc(in SweepInput) job.Func {
	raw, err := json.Marshal(in)
	if err != nil {
		return func(context.Context, func(job.Progress)) ([]byte, error) {
			return nil, fmt.Errorf("broker: encode sweep input: %w", ErrInvalid)
		}
	}
	detail := json.RawMessage(raw)
	return func(ctx context.Context, progress func(job.Progress)) ([]byte, error) {
		progress(job.Progress{Stage: "list", Detail: detail})
		out, err := s.Sweep(ctx, in)
		if err != nil {
			progress(job.Progress{Stage: "error", Detail: detail})
			data, _ := json.Marshal(out)
			return data, err
		}
		progress(job.Progress{Stage: "done", Detail: detail})
		data, merr := json.Marshal(out)
		if merr != nil {
			return nil, fmt.Errorf("broker: encode sweep result: %w", ErrSweep)
		}
		return data, nil
	}
}

// Sweep ends every session row still open past its cap plus the margin.
// It settles each one through the reconciler, deletes the provider record
// after, and records the outcome. A settled session is skipped, so a
// repeat pass moves no money and raises no alert.
func (s *Sweeper) Sweep(ctx context.Context, in SweepInput) ([]SweepOutcome, error) {
	margin := in.MarginSeconds
	if margin < 0 {
		margin = s.margin
	}
	listed, err := s.source.ListOpen(ctx)
	if err != nil {
		return nil, fmt.Errorf("broker: sweep: list open sessions: %w: %w", ErrSweep, err)
	}
	out := make([]SweepOutcome, 0, len(listed))
	for _, candidate := range listed {
		one, err := s.sweepOne(ctx, candidate, margin)
		if err != nil {
			return out, err
		}
		out = append(out, one)
	}
	return out, nil
}

// sweepOne ends and settles one candidate. Young rows and settled rows are
// skipped with the reason on the outcome.
func (s *Sweeper) sweepOne(ctx context.Context, candidate Candidate, margin int) (SweepOutcome, error) {
	if err := validateCandidate(candidate); err != nil {
		return SweepOutcome{}, err
	}
	if candidate.OpenSeconds <= candidate.TokenCapSeconds+margin {
		return SweepOutcome{
			SessionID: candidate.SessionID,
			Skipped:   true,
			Detail:    fmt.Sprintf("session open %d seconds under a %d second cap plus %d second margin", candidate.OpenSeconds, candidate.TokenCapSeconds, margin),
		}, nil
	}
	settled, err := s.reconciler.IsSettled(ctx, candidate.SessionID)
	if err != nil {
		return SweepOutcome{}, err
	}
	if settled {
		if err := s.markSwept(ctx, candidate.SessionID, "", 0, false, "session already settled"); err != nil {
			return SweepOutcome{}, err
		}
		return SweepOutcome{SessionID: candidate.SessionID, Skipped: true, Detail: "session already settled"}, nil
	}
	status, err := s.statuses.ReadStatus(ctx, candidate.ProviderSessionID)
	if err != nil {
		if errors.Is(err, ErrProviderGone) {
			return s.reviewGone(ctx, candidate)
		}
		return SweepOutcome{}, fmt.Errorf("broker: sweep: read session %s: %w: %w", candidate.ProviderSessionID, ErrSweep, err)
	}
	seconds := status.DurationSeconds
	stillOpen := !status.HasDuration
	if stillOpen {
		seconds = status.OpenSeconds
		if seconds < 0 {
			seconds = 0
		}
	}
	res, err := s.reconciler.ReconcileAbandoned(ctx, toInput(candidate), AbandonedSession{
		DurationSeconds: seconds,
		RecordingURL:    status.RecordingURL,
		TimelineURL:     status.TimelineURL,
	})
	if err != nil {
		return SweepOutcome{}, err
	}
	if res.NeedsReview {
		return SweepOutcome{}, fmt.Errorf("broker: sweep: settle asked for review: %w: %s", ErrSweep, res.ReviewDetail)
	}
	ended, err := s.ender.EndSession(ctx, candidate.ProviderSessionID)
	if err != nil {
		return SweepOutcome{}, fmt.Errorf("broker: sweep: end session %s: %w: %w", candidate.ProviderSessionID, ErrSweep, err)
	}
	detail := fmt.Sprintf("settled %d seconds at %s, provider record %s", res.ConnectedSeconds, res.Cost, ended.Detail)
	if stillOpen {
		detail += ", session still open at settle so spend may run on"
		if err := s.alert(ctx, candidate, AlertSweepOpen, detail); err != nil {
			detail += " (alert failed: " + err.Error() + ")"
		}
	}
	if err := s.markSwept(ctx, candidate.SessionID, status.Status, res.ConnectedSeconds, false, detail); err != nil {
		return SweepOutcome{}, err
	}
	return SweepOutcome{
		SessionID:        candidate.SessionID,
		ProviderStatus:   status.Status,
		ConnectedSeconds: res.ConnectedSeconds,
		Cost:             res.Cost,
		ServerEnded:      false,
		Detail:           detail,
	}, nil
}

// reviewGone stops a sweep whose provider record is already gone. The
// duration is unknowable, so the sweep settles nothing and asks for
// review, the same way an ambiguous resume settles nothing.
func (s *Sweeper) reviewGone(ctx context.Context, candidate Candidate) (SweepOutcome, error) {
	detail := fmt.Sprintf("provider record for session %s is gone, duration unknowable so nothing settled", candidate.SessionID)
	if err := s.alert(ctx, candidate, AlertNeedsReview, detail); err != nil {
		detail += " (alert failed: " + err.Error() + ")"
	}
	if err := s.markSwept(ctx, candidate.SessionID, "gone", 0, false, detail); err != nil {
		return SweepOutcome{}, err
	}
	return SweepOutcome{SessionID: candidate.SessionID, ProviderStatus: "gone", Detail: detail}, nil
}

// alert delivers one alert through the alerter when set. It returns the
// delivery failure so the caller records it beside the outcome. It writes
// no row itself, so the outcome keeps its real status and seconds.
func (s *Sweeper) alert(ctx context.Context, candidate Candidate, kind AlertKind, detail string) error {
	if s.alerter == nil {
		return nil
	}
	if err := s.alerter.Report(ctx, Alert{Kind: kind, SessionID: candidate.SessionID, OwnerID: candidate.OwnerID, Detail: detail}); err != nil {
		return err
	}
	return nil
}

// markSwept records the outcome on the sweep row. The first write wins, so
// a repeat pass never overwrites what the first pass recorded.
func (s *Sweeper) markSwept(ctx context.Context, sessionID, providerStatus string, seconds int, serverEnded bool, detail string) error {
	ended := 0
	if serverEnded {
		ended = 1
	}
	if _, err := s.db.Writer().ExecContext(ctx,
		`INSERT OR IGNORE INTO sweep_state (session_id, provider_status, connected_seconds, server_ended, detail, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		sessionID, providerStatus, seconds, ended, detail, s.now().UnixMilli()); err != nil {
		return fmt.Errorf("broker: mark session swept: %w", ErrSweep)
	}
	return nil
}

// validateCandidate rejects a candidate the sweep cannot settle.
func validateCandidate(candidate Candidate) error {
	if candidate.SessionID == "" || candidate.OwnerID == "" || candidate.EpisodeID == "" {
		return fmt.Errorf("broker: sweep: %w: session, owner, and episode must not be empty", ErrInvalid)
	}
	if candidate.ProviderSessionID == "" {
		return fmt.Errorf("broker: sweep: %w: provider session id must not be empty", ErrInvalid)
	}
	if candidate.LeaseID == "" {
		return fmt.Errorf("broker: sweep: %w: lease id must not be empty", ErrInvalid)
	}
	if candidate.Reservation.ID == "" {
		return fmt.Errorf("broker: sweep: %w: reservation must name a hold", ErrInvalid)
	}
	if candidate.TokenCapSeconds <= 0 {
		return fmt.Errorf("broker: sweep: %w: session cap must be positive", ErrInvalid)
	}
	if candidate.OpenSeconds < 0 {
		return fmt.Errorf("broker: sweep: %w: open age must not be negative", ErrInvalid)
	}
	return nil
}

// toInput carries the mint time linkage into the reconciler.
func toInput(candidate Candidate) Input {
	return Input{
		SessionID:         candidate.SessionID,
		OwnerID:           candidate.OwnerID,
		EpisodeID:         candidate.EpisodeID,
		ProviderSessionID: candidate.ProviderSessionID,
		LeaseID:           candidate.LeaseID,
		Reservation:       candidate.Reservation,
		TokenCapSeconds:   candidate.TokenCapSeconds,
	}
}
