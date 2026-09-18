// Package broker starts live sessions behind seven ordered checks.
//
// The broker mints one single-use provider token per honest request. Every
// refusal stops at the first failed check and holds nothing: no second
// token, no budget hold, no lease, no row. The checks run in this order:
// the spend gate, the runtime kill switch, the guest session quota, the
// owner and global ceilings reserved for the full session cap, the session
// lease, the provider token call, and finally the diary rows with the
// response. A reservation settles later when reconciliation reads the real
// connected seconds.
package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/nrynss/keel/cost"
	costsqlitestore "github.com/nrynss/keel/cost/sqlitestore"
	"github.com/nrynss/keel/flag"
	"github.com/nrynss/keel/gate"
	"github.com/nrynss/keel/lease"
	"github.com/nrynss/keel/wire"
	"github.com/nrynss/reprise/internal/assemblyai"
	"github.com/nrynss/reprise/internal/identity"
)

// KillSwitch is the runtime flag that stops minting tokens at once. It reads
// at request time, so an operator flip lands without a restart.
var KillSwitch = flag.Bool{
	Name:    "sessions_paused",
	Default: false,
	Help:    "Stop minting live session tokens at once without a restart.",
}

// Rate and cap bounds for one session.
const (
	// minSessionCapSeconds and maxSessionCapSeconds bound the session cap.
	// They match the provider token bounds, so the cap the budget reserves
	// for is always a cap the provider honours.
	minSessionCapSeconds = 60
	maxSessionCapSeconds = 10800
	// ratePerSecondNanodollars prices one connected second at $4.50 per
	// hour. The quotient is exact, so the estimate needs no rounding.
	ratePerSecondNanodollars = cost.Price(1250000)
)

// Refusal codes. Screens branch on these and never on wording.
const (
	// CodeNoOwner answers a request with no guest behind it.
	CodeNoOwner = "session_required"
	// CodePaused answers while the kill switch is set.
	CodePaused = "sessions_paused"
	// CodeQuota answers a guest past its session count.
	CodeQuota = "guest_quota_reached"
	// CodeBudget answers when either ceiling refuses the reservation.
	CodeBudget = "budget_exhausted"
	// CodeSlots answers when no lease slot is free.
	CodeSlots = "too_many_sessions"
	// CodeProvider answers when the token call fails.
	CodeProvider = "session_provider_unavailable"
	// CodeInternal answers when the diary or a dependency breaks.
	CodeInternal = "internal_error"
)

// Sentinels, one per refusal condition.
var (
	// ErrInvalid reports a Config the broker cannot honour.
	ErrInvalid = errors.New("broker: invalid config")
	// ErrNoOwner reports a request with no guest behind it.
	ErrNoOwner = errors.New("broker: request carries no session owner")
	// ErrPaused reports a mint refused by the kill switch.
	ErrPaused = errors.New("broker: live sessions are paused")
	// ErrQuota reports a guest past its session count.
	ErrQuota = errors.New("broker: guest session quota is reached")
	// ErrBudget reports a reservation either ceiling refused.
	ErrBudget = errors.New("broker: spend ceiling is reached")
	// ErrSlots reports a lease open with no free slot.
	ErrSlots = errors.New("broker: no session slot is free")
	// ErrProvider reports a failed token call.
	ErrProvider = errors.New("broker: session provider failed")
	// ErrStore reports a diary or dependency failure mid-mint.
	ErrStore = errors.New("broker: session store failed")
)

// SessionConfig is the session setup the response carries beside the token.
// The host prompt task fills it from stored rows. Counts and names in it
// always come from those rows, never from invention.
type SessionConfig struct {
	// SystemPrompt voices the host and its interview style.
	SystemPrompt string `json:"system_prompt"`
	// Greeting opens the episode on the planted callback.
	Greeting string `json:"greeting"`
	// Keyterms holds up to 100 recurring names for recognition.
	Keyterms []string `json:"keyterms"`
}

// ConfigBuilder builds the session config from stored rows. The host prompt
// task implements it. A stub serves until then.
type ConfigBuilder interface {
	// BuildSessionConfig returns the config for one owner from stored rows.
	BuildSessionConfig(ctx context.Context, ownerID string) (SessionConfig, error)
}

// TokenMinter mints one single-use provider token capped at the given
// seconds. The provider client implements it.
type TokenMinter interface {
	// Mint returns one single-use token capped at maxSessionSeconds.
	Mint(ctx context.Context, maxSessionSeconds int) (string, error)
}

// Budget reserves session spend on the durable ceilings. The keyed budget
// store implements it.
type Budget interface {
	// Reserve holds estimate against the owner and global ceilings.
	Reserve(ctx context.Context, owner string, estimate cost.Price) (costsqlitestore.Reservation, error)
	// Release frees a hold the caller never spent.
	Release(ctx context.Context, owner string, r costsqlitestore.Reservation) error
	// SetLimit gives an owner its own ceiling.
	SetLimit(ctx context.Context, owner string, limit cost.Price) error
}

// Lessor opens time-capped leases on metered sessions. The lease manager
// implements it.
type Lessor interface {
	// Open starts one lease for the owner at the estimate.
	Open(ctx context.Context, owner string, estimate cost.Price, kill string) (lease.Lease, error)
	// Close ends one open lease at the reported price.
	Close(ctx context.Context, id string, price cost.Price) (lease.Lease, error)
	// Active reports how many quota slots stay held.
	Active() int
}

// Session is the 201 body of a started session. It carries the token and
// the config together, so the page dials with one round trip. It never
// carries the provider key.
type Session struct {
	// SessionID identifies the diary session row.
	SessionID string `json:"session_id"`
	// EpisodeID identifies the diary episode the session records.
	EpisodeID string `json:"episode_id"`
	// Token is the single-use provider token, expiring 60 seconds after mint.
	Token string `json:"token"`
	// ExpiresInSeconds echoes the token lifetime.
	ExpiresInSeconds int `json:"expires_in_seconds"`
	// MaxSessionDurationSeconds echoes the session cap.
	MaxSessionDurationSeconds int `json:"max_session_duration_seconds"`
	// Config is the session setup built from stored rows.
	Config SessionConfig `json:"config"`
}

var (
	_ TokenMinter = (*assemblyai.Client)(nil)
	_ Budget      = (*costsqlitestore.KeyedBudget)(nil)
	_ Lessor      = (*lease.Manager)(nil)
)

// passMeter runs the lease open and close bookkeeping without holding
// budget. The broker reserves the full session cap on the durable ceilings
// before it opens the lease, so the manager seam must not hold a second
// reservation on the same estimate.
type passMeter struct{}

// Call runs the work and returns its usage without reserving or booking.
func (passMeter) Call(ctx context.Context, _ cost.Price, _, _ string, work cost.Work) (cost.Usage, error) {
	if err := ctx.Err(); err != nil {
		return cost.Usage{}, err
	}
	usage, err := work(ctx)
	if err != nil {
		return cost.Usage{}, err
	}
	return usage, nil
}

// Config carries everything the broker needs. The zero value is not usable.
type Config struct {
	// Flags reads the kill switch at request time. It must not be nil.
	Flags flag.Store
	// Budgets holds the owner and global ceilings. It must not be nil.
	Budgets Budget
	// LeaseQuota bounds how many leases stay open at once. It must not be nil.
	LeaseQuota *lease.Quota
	// LeaseStore keeps the lease rows. It must not be nil.
	LeaseStore lease.Store
	// Minter mints the provider token. It must not be nil.
	Minter TokenMinter
	// Sessions builds the session config. It must not be nil.
	Sessions ConfigBuilder
	// Diary persists the session rows. It must not be nil.
	Diary Diary
	// SessionCapSeconds caps one live session. It must sit within 60 to 10800.
	SessionCapSeconds int
	// GuestMaxSessions caps how many sessions one guest may start. It must be positive.
	GuestMaxSessions int
	// OwnerSessionLimit is the ceiling ensured for an owner with none yet. It must be positive.
	OwnerSessionLimit cost.Price
}

// Broker starts live sessions behind the ordered checks. Create it with New,
// because the zero value holds no stores. A Broker is safe for concurrent
// use.
type Broker struct {
	flags      flag.Store
	budgets    Budget
	leases     *lease.Manager
	minter     TokenMinter
	sessions   ConfigBuilder
	diary      Diary
	cap        int
	guestMax   int
	ownerLimit cost.Price
}

// New validates cfg and returns the broker. It builds the lease manager over
// the quota and store with a bookkeeping meter, because the durable budget
// hold already covers the estimate.
func New(cfg Config) (*Broker, error) {
	if cfg.Flags == nil {
		return nil, fmt.Errorf("broker: new: %w: flags store must not be nil", ErrInvalid)
	}
	if cfg.Budgets == nil {
		return nil, fmt.Errorf("broker: new: %w: budgets must not be nil", ErrInvalid)
	}
	if cfg.LeaseQuota == nil {
		return nil, fmt.Errorf("broker: new: %w: lease quota must not be nil", ErrInvalid)
	}
	if cfg.LeaseStore == nil {
		return nil, fmt.Errorf("broker: new: %w: lease store must not be nil", ErrInvalid)
	}
	if cfg.Minter == nil {
		return nil, fmt.Errorf("broker: new: %w: minter must not be nil", ErrInvalid)
	}
	if cfg.Sessions == nil {
		return nil, fmt.Errorf("broker: new: %w: session config builder must not be nil", ErrInvalid)
	}
	if cfg.Diary == nil {
		return nil, fmt.Errorf("broker: new: %w: diary must not be nil", ErrInvalid)
	}
	if cfg.SessionCapSeconds < minSessionCapSeconds || cfg.SessionCapSeconds > maxSessionCapSeconds {
		return nil, fmt.Errorf("broker: new: %w: session cap must sit within 60 to 10800 seconds", ErrInvalid)
	}
	if cfg.GuestMaxSessions <= 0 {
		return nil, fmt.Errorf("broker: new: %w: guest session cap must be positive", ErrInvalid)
	}
	if cfg.OwnerSessionLimit <= 0 {
		return nil, fmt.Errorf("broker: new: %w: owner session limit must be positive", ErrInvalid)
	}
	manager, err := lease.New(lease.Config{
		Quota: cfg.LeaseQuota,
		Meter: passMeter{},
		Store: cfg.LeaseStore,
		Cap:   time.Duration(cfg.SessionCapSeconds) * time.Second,
		Kind:  "session",
	})
	if err != nil {
		return nil, fmt.Errorf("broker: new lease manager: %w", ErrInvalid)
	}
	return &Broker{
		flags:      cfg.Flags,
		budgets:    cfg.Budgets,
		leases:     manager,
		minter:     cfg.Minter,
		sessions:   cfg.Sessions,
		diary:      cfg.Diary,
		cap:        cfg.SessionCapSeconds,
		guestMax:   cfg.GuestMaxSessions,
		ownerLimit: cfg.OwnerSessionLimit,
	}, nil
}

// Leases returns the lease manager, so reconciliation settles the same rows
// the broker opens.
func (b *Broker) Leases() *lease.Manager { return b.leases }

// Budgets returns the ceilings, so reconciliation settles the same holds
// the broker takes.
func (b *Broker) Budgets() Budget { return b.budgets }

// Diary returns the diary, so later passes read the rows the broker wrote.
func (b *Broker) Diary() Diary { return b.diary }

// Cap returns the session cap in seconds.
func (b *Broker) Cap() int { return b.cap }

// Route wraps the broker in the spend gate. Mount the result at POST
// /api/sessions behind the guest middleware, with the gate outermost.
func (b *Broker) Route(g *gate.Gate, rule gate.Rule) (http.Handler, error) {
	if g == nil {
		return nil, fmt.Errorf("broker: route: %w: gate must not be nil", ErrInvalid)
	}
	return g.Protect(rule, b)
}

// ServeHTTP starts one session. It answers POST only, through the shared
// error envelope on every refusal.
func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeRefusal(w, http.StatusMethodNotAllowed, "method_not_allowed", "sessions start with POST")
		return
	}
	b.create(w, r)
}

// create runs the ordered checks and stops at the first refusal. Every path
// after the reservation releases it, and every path after the lease closes
// it, so a refusal holds nothing.
func (b *Broker) create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := identity.UserFromContext(ctx)
	if !ok || user.ID == "" {
		writeRefusal(w, http.StatusUnauthorized, CodeNoOwner, "this call needs a guest session")
		return
	}
	owner := user.ID

	paused, _, err := b.flags.Bool(ctx, KillSwitch)
	if err != nil {
		writeRefusal(w, http.StatusInternalServerError, CodeInternal, "the switch could not be read")
		return
	}
	if paused {
		writeRefusal(w, http.StatusServiceUnavailable, CodePaused, "live sessions are paused")
		return
	}

	held, err := b.diary.CountSessions(ctx, owner)
	if err != nil {
		writeRefusal(w, http.StatusInternalServerError, CodeInternal, "the session count could not be read")
		return
	}
	if held >= b.guestMax {
		writeRefusal(w, http.StatusForbidden, CodeQuota, "this guest started all its sessions")
		return
	}

	estimate, err := estimateForCap(b.cap)
	if err != nil {
		writeRefusal(w, http.StatusInternalServerError, CodeInternal, "the session price could not be priced")
		return
	}
	reservation, err := b.reserve(ctx, owner, estimate)
	if err != nil {
		if errors.Is(err, ErrBudget) {
			writeRefusal(w, http.StatusServiceUnavailable, CodeBudget, "today's spend ceiling is reached")
			return
		}
		writeRefusal(w, http.StatusInternalServerError, CodeInternal, "the spend hold could not be taken")
		return
	}

	opened, err := b.leases.Open(ctx, owner, estimate, "")
	if err != nil {
		_ = b.budgets.Release(ctx, owner, reservation)
		if errors.Is(err, lease.ErrQuota) {
			writeRefusal(w, http.StatusTooManyRequests, CodeSlots, "every session slot is busy")
			return
		}
		writeRefusal(w, http.StatusInternalServerError, CodeInternal, "the session lease could not open")
		return
	}
	cleanup := func() {
		_, _ = b.leases.Close(ctx, opened.ID, 0)
		_ = b.budgets.Release(ctx, owner, reservation)
	}

	token, err := b.minter.Mint(ctx, b.cap)
	if err != nil {
		cleanup()
		writeRefusal(w, http.StatusBadGateway, CodeProvider, "the provider would not mint a token")
		return
	}
	sessionConfig, err := b.sessions.BuildSessionConfig(ctx, owner)
	if err != nil {
		cleanup()
		writeRefusal(w, http.StatusInternalServerError, CodeInternal, "the session config could not be built")
		return
	}
	episodeID, sessionID, err := b.diary.CreateEpisodeAndSession(ctx, owner, b.cap)
	if err != nil {
		cleanup()
		writeRefusal(w, http.StatusInternalServerError, CodeInternal, "the session row could not be written")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(Session{
		SessionID:                 sessionID,
		EpisodeID:                 episodeID,
		Token:                     token,
		ExpiresInSeconds:          assemblyai.TokenExpirySeconds,
		MaxSessionDurationSeconds: b.cap,
		Config:                    sessionConfig,
	})
}

// reserve holds estimate against the owner and global ceilings. An owner
// with no ceiling yet gets the configured default first, so a first visit
// needs no separate provisioning.
func (b *Broker) reserve(ctx context.Context, owner string, estimate cost.Price) (costsqlitestore.Reservation, error) {
	reservation, err := b.budgets.Reserve(ctx, owner, estimate)
	if errors.Is(err, cost.ErrUnknownOwner) {
		if setErr := b.budgets.SetLimit(ctx, owner, b.ownerLimit); setErr != nil {
			return costsqlitestore.Reservation{}, fmt.Errorf("broker: ensure owner ceiling: %w", ErrStore)
		}
		reservation, err = b.budgets.Reserve(ctx, owner, estimate)
	}
	if errors.Is(err, cost.ErrOverBudget) {
		return costsqlitestore.Reservation{}, fmt.Errorf("broker: reserve session spend: %w", ErrBudget)
	}
	if err != nil {
		return costsqlitestore.Reservation{}, fmt.Errorf("broker: reserve session spend: %w", ErrStore)
	}
	return reservation, nil
}

// estimateForCap prices the full session cap at $4.50 per connected hour.
func estimateForCap(capSeconds int) (cost.Price, error) {
	if capSeconds <= 0 {
		return 0, fmt.Errorf("broker: price cap %d: %w: cap must be positive", capSeconds, ErrInvalid)
	}
	estimate := ratePerSecondNanodollars * cost.Price(capSeconds)
	if cost.Price(capSeconds) != 0 && estimate/cost.Price(capSeconds) != ratePerSecondNanodollars {
		return 0, fmt.Errorf("broker: price cap %d: %w: estimate leaves the int64 range", capSeconds, ErrInvalid)
	}
	return estimate, nil
}

// writeRefusal answers through the shared error envelope. Screens branch on
// the code and never on the message.
func writeRefusal(w http.ResponseWriter, status int, code, message string) {
	_ = wire.WriteError(w, status, code, message, nil)
}
