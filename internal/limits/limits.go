// Package limits holds the guest caps, the spend ceiling view, and the
// runtime switch that stops live sessions at once.
//
// The session broker remains the single enforcement point. It reads the
// switch at request time and counts guest sessions before it reserves
// spend, so a flip here lands on the next mint with no restart. This
// package declares the switch under the same name the broker reads,
// answers what the caps are, reports spend from the Keel budget stores,
// and serves the admin endpoints the owner page calls.
//
// Spend is always ceiling minus headroom, both from Keel. The global
// figure reads the store ceiling and its headroom directly. The per-owner
// figure reads the headroom from the keyed budget and subtracts it from
// the ceiling Reprise set through SetLimit. The ledger carries no charge
// timestamps, so no figure here spans days and none claims a history.
package limits

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/nrynss/keel/cost"
	costsqlitestore "github.com/nrynss/keel/cost/sqlitestore"
	"github.com/nrynss/keel/flag"
	"github.com/nrynss/reprise/internal/broker"
)

// SessionsPaused is the runtime flag that stops minting live session
// tokens at once. It aliases the declaration the broker checks on every
// mint, so the admin switch and the mint check cannot name different
// flags. A flip reads on the next request without a restart.
var SessionsPaused = broker.KillSwitch

// Refusal and state codes. Screens branch on these and never on wording.
// Each aliases the broker code it mirrors, so the admin surface and the
// mint refusals cannot drift apart.
const (
	// CodeSessionsPaused mirrors the mint refusal while the switch is set.
	CodeSessionsPaused = broker.CodePaused
	// CodeGuestLimit mirrors the mint refusal past the guest session cap.
	CodeGuestLimit = broker.CodeQuota
	// CodeInternal answers when a dependency breaks mid-call.
	CodeInternal = broker.CodeInternal
	// CodeOwnerRequired answers an admin call with no owner behind it.
	// The owner login that mints that proof is still open, so the stub
	// below answers it on every call until the login lands.
	CodeOwnerRequired = "owner_required"
	// CodeUnknownOwner answers a spend lookup for an owner with no
	// ceiling. A first visit provisions its ceiling on its first mint,
	// so an unknown owner here simply has not started a session yet.
	CodeUnknownOwner = "unknown_owner"
	// CodeMethodNotAllowed answers a non-GET or non-POST call.
	CodeMethodNotAllowed = "method_not_allowed"
	// CodeInvalidRequest answers a call whose body or owner is unusable.
	CodeInvalidRequest = "invalid_request"
)

// Session cap bounds in seconds. They match the provider token bounds,
// so the cap the budget reserves for is always one the provider honours.
const (
	minSessionCapSeconds = 60
	maxSessionCapSeconds = 10800
)

// Sentinels, one per failure condition.
var (
	// ErrInvalid reports a Config the service cannot honour.
	ErrInvalid = errors.New("limits: invalid config")
	// ErrStore reports a flag, budget, or ledger failure mid-call.
	ErrStore = errors.New("limits: limit store failed")
	// ErrGuestLimit reports a guest past its session count.
	ErrGuestLimit = errors.New("limits: guest session cap is reached")
)

// Budget reserves session spend on the durable ceilings. The keyed budget
// store implements it.
type Budget interface {
	// Reserve holds estimate against the owner and global ceilings.
	Reserve(ctx context.Context, owner string, estimate cost.Price) (costsqlitestore.Reservation, error)
	// Release frees a hold the caller never spent.
	Release(ctx context.Context, owner string, r costsqlitestore.Reservation) error
	// Settle books actual spend against a hold.
	Settle(ctx context.Context, owner string, r costsqlitestore.Reservation, actual cost.Price) error
	// SetLimit gives an owner its own ceiling.
	SetLimit(ctx context.Context, owner string, limit cost.Price) error
	// Remaining reports the headroom left under the owner's ceiling.
	Remaining(ctx context.Context, owner string) (cost.Price, error)
}

// GlobalBudget reads the store-wide ceiling and its headroom. The cost
// store implements it.
type GlobalBudget interface {
	// Limit reports the store-wide ceiling.
	Limit(ctx context.Context) (cost.Price, error)
	// Remaining reports the headroom left under that ceiling.
	Remaining(ctx context.Context) (cost.Price, error)
}

// Spending carries one spend figure. Every field is nanodollars from the
// Keel ledger, and Spent is always Ceiling minus Remaining. The ceiling
// is a daily one, so the figure is today's spend, never a history.
type Spending struct {
	// CeilingND is the ceiling the figure subtracts from, in nanodollars.
	CeilingND int64 `json:"ceiling_nd"`
	// RemainingND is the headroom Keel reports, in nanodollars.
	RemainingND int64 `json:"remaining_nd"`
	// SpentND is CeilingND minus RemainingND, in nanodollars.
	SpentND int64 `json:"spent_nd"`
}

// Caps carries the guest caps for one environment. Each comes from
// settings, so each changes without a deploy.
type Caps struct {
	// GuestMaxSessions caps how many sessions one guest may start.
	GuestMaxSessions int `json:"guest_max_sessions"`
	// SessionMaxSeconds caps one live session in seconds.
	SessionMaxSeconds int `json:"session_max_seconds"`
	// DailySpendCents caps provider spend per day in cents.
	DailySpendCents int64 `json:"daily_spend_cents"`
}

// Config carries everything the service needs. The zero value is not usable.
type Config struct {
	// Flags reads and flips the switch. It must not be nil.
	Flags flag.Store
	// Budgets holds the owner and global ceilings. It must not be nil.
	Budgets Budget
	// Global reads the store-wide ceiling. It must not be nil.
	Global GlobalBudget
	// Auth gates the admin endpoints. It must not be nil.
	Auth OwnerAuth
	// GuestMaxSessions caps how many sessions one guest may start. It must be positive.
	GuestMaxSessions int
	// SessionMaxSeconds caps one live session. It must sit within 60 to 10800.
	SessionMaxSeconds int
	// DailySpendCents caps provider spend per day in cents. It must not be negative.
	DailySpendCents int64
	// OwnerDefaultLimit is the ceiling a first mint provisions for an
	// owner with none yet. It must be positive, and it must equal the
	// default the broker ensures, or the per-owner figure disagrees
	// with the ceiling the mint actually reserved against.
	OwnerDefaultLimit cost.Price
}

// Service answers the caps, the switch, and spend from the Keel stores.
// Create it with New, because the zero value holds no stores. A Service
// is safe for concurrent use.
type Service struct {
	flags      flag.Store
	budgets    Budget
	global     GlobalBudget
	auth       OwnerAuth
	caps       Caps
	ownerLimit cost.Price
	mu         sync.Mutex
	overrides  map[string]cost.Price
}

// New validates cfg and returns the service. The auth seam stays explicit:
// pass the stub until the owner login lands, then swap it without
// touching anything else here.
func New(cfg Config) (*Service, error) {
	if cfg.Flags == nil {
		return nil, fmt.Errorf("limits: new: %w: flags store must not be nil", ErrInvalid)
	}
	if cfg.Budgets == nil {
		return nil, fmt.Errorf("limits: new: %w: budgets must not be nil", ErrInvalid)
	}
	if cfg.Global == nil {
		return nil, fmt.Errorf("limits: new: %w: global budget must not be nil", ErrInvalid)
	}
	if cfg.Auth == nil {
		return nil, fmt.Errorf("limits: new: %w: owner auth must not be nil", ErrInvalid)
	}
	if cfg.GuestMaxSessions <= 0 {
		return nil, fmt.Errorf("limits: new: %w: guest session cap must be positive", ErrInvalid)
	}
	if cfg.SessionMaxSeconds < minSessionCapSeconds || cfg.SessionMaxSeconds > maxSessionCapSeconds {
		return nil, fmt.Errorf("limits: new: %w: session cap must sit within 60 to 10800 seconds", ErrInvalid)
	}
	if cfg.DailySpendCents < 0 {
		return nil, fmt.Errorf("limits: new: %w: daily spend ceiling must not be negative", ErrInvalid)
	}
	if cfg.OwnerDefaultLimit <= 0 {
		return nil, fmt.Errorf("limits: new: %w: owner default limit must be positive", ErrInvalid)
	}
	return &Service{
		flags:   cfg.Flags,
		budgets: cfg.Budgets,
		global:  cfg.Global,
		auth:    cfg.Auth,
		caps: Caps{
			GuestMaxSessions:  cfg.GuestMaxSessions,
			SessionMaxSeconds: cfg.SessionMaxSeconds,
			DailySpendCents:   cfg.DailySpendCents,
		},
		ownerLimit: cfg.OwnerDefaultLimit,
		overrides:  map[string]cost.Price{},
	}, nil
}

// Caps returns the guest caps from settings.
func (s *Service) Caps() Caps { return s.caps }

// Paused reports whether the switch currently stops minting. It reads at
// call time, so a flip lands without a restart.
func (s *Service) Paused(ctx context.Context) (bool, error) {
	paused, _, err := s.flags.Bool(ctx, SessionsPaused)
	if err != nil {
		return false, fmt.Errorf("limits: read switch: %w", ErrStore)
	}
	return paused, nil
}

// SetPaused flips the switch at once. The broker reads the same flag on
// its next mint, so no restart stands between the flip and the refusal.
func (s *Service) SetPaused(ctx context.Context, paused bool) error {
	if _, err := s.flags.SetBool(ctx, SessionsPaused, paused); err != nil {
		return fmt.Errorf("limits: flip switch: %w", ErrStore)
	}
	return nil
}

// CheckGuestSessions reports whether a guest with count started sessions
// may start another. The broker enforces this on the mint path. This
// helper lets the admin surface name the same refusal with the same code.
func (s *Service) CheckGuestSessions(count int) error {
	if count < 0 {
		return fmt.Errorf("limits: check guest sessions %d: %w: count must not be negative", count, ErrInvalid)
	}
	if count >= s.caps.GuestMaxSessions {
		return fmt.Errorf("limits: guest with %d sessions: %w", count, ErrGuestLimit)
	}
	return nil
}

// SetOwnerLimit gives owner a new ceiling and remembers it for the spend
// figure. Later reads subtract the headroom from this value, which is the
// ceiling the keyed budget enforces.
func (s *Service) SetOwnerLimit(ctx context.Context, owner string, limit cost.Price) error {
	if owner == "" {
		return fmt.Errorf("limits: set owner limit: %w: owner must not be empty", ErrInvalid)
	}
	if limit < 0 {
		return fmt.Errorf("limits: set owner limit: %w: limit must not be negative", ErrInvalid)
	}
	if err := s.budgets.SetLimit(ctx, owner, limit); err != nil {
		return fmt.Errorf("limits: set owner limit: %w", ErrStore)
	}
	s.mu.Lock()
	s.overrides[owner] = limit
	s.mu.Unlock()
	return nil
}

// ownerCeiling returns the ceiling OwnerSpend subtracts from. An admin
// edit through SetOwnerLimit wins. Otherwise the first-mint default
// applies, which matches the ceiling the broker provisions.
func (s *Service) ownerCeiling(owner string) cost.Price {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit, ok := s.overrides[owner]; ok {
		return limit
	}
	return s.ownerLimit
}

// OwnerSpend reports one owner's spend as ceiling minus headroom. The
// headroom comes from the keyed budget. It reports an error matching
// cost.ErrUnknownOwner when no ceiling was ever set for the owner.
func (s *Service) OwnerSpend(ctx context.Context, owner string) (Spending, error) {
	if owner == "" {
		return Spending{}, fmt.Errorf("limits: owner spend: %w: owner must not be empty", ErrInvalid)
	}
	remaining, err := s.budgets.Remaining(ctx, owner)
	if err != nil {
		if errors.Is(err, cost.ErrUnknownOwner) {
			return Spending{}, fmt.Errorf("limits: owner spend %q: %w", owner, err)
		}
		return Spending{}, fmt.Errorf("limits: owner spend %q: %w", owner, ErrStore)
	}
	return spendOf(s.ownerCeiling(owner), remaining)
}

// GlobalSpend reports the store-wide spend as ceiling minus headroom.
// Both come from the cost store, so the figure tracks the settled
// reservations exactly.
func (s *Service) GlobalSpend(ctx context.Context) (Spending, error) {
	ceiling, err := s.global.Limit(ctx)
	if err != nil {
		return Spending{}, fmt.Errorf("limits: global spend: %w", ErrStore)
	}
	remaining, err := s.global.Remaining(ctx)
	if err != nil {
		return Spending{}, fmt.Errorf("limits: global spend: %w", ErrStore)
	}
	return spendOf(ceiling, remaining)
}

// spendOf returns ceiling minus remaining. The headroom never exceeds its
// ceiling, so a negative result means the ledger disagrees with the
// ceiling and the figure refuses instead of printing a negative spend.
func spendOf(ceiling, remaining cost.Price) (Spending, error) {
	spent := int64(ceiling) - int64(remaining)
	if (remaining > 0 && spent > int64(ceiling)) || (remaining < 0 && spent < int64(ceiling)) {
		return Spending{}, fmt.Errorf("limits: spend %d minus %d: %w: figure leaves the int64 range", int64(ceiling), int64(remaining), ErrStore)
	}
	if spent < 0 {
		return Spending{}, fmt.Errorf("limits: spend %d minus %d: %w: headroom exceeds its ceiling", int64(ceiling), int64(remaining), ErrStore)
	}
	return Spending{CeilingND: int64(ceiling), RemainingND: int64(remaining), SpentND: spent}, nil
}

var (
	_ Budget       = (*costsqlitestore.KeyedBudget)(nil)
	_ GlobalBudget = (*costsqlitestore.Store)(nil)
)
