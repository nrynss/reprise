// Pass and refusal pins for the caps, the switch, and the spend figures.
// Every spend figure here runs against the real Keel stores over a temp
// database, so the pins measure the ledger rather than a copy of it.
package limits

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/nrynss/keel/cost"
	costsqlitestore "github.com/nrynss/keel/cost/sqlitestore"
	flagsqlitestore "github.com/nrynss/keel/flag/sqlitestore"
	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/broker"
)

const (
	testGuestMax     = 4
	testCapSeconds   = 1800
	testDailyCents   = 2000
	testGlobalLimit  = cost.Price(100000000000)
	testOwnerLimit   = cost.Price(10000000000)
	testSettled      = cost.Price(1250000 * 1800)
	testOwner        = "owner-probe-1"
	testUnknownOwner = "owner-probe-unknown"
)

type stores struct {
	flags   *flagsqlitestore.Store
	costs   *costsqlitestore.Store
	budgets *costsqlitestore.KeyedBudget
}

func openStores(t *testing.T, globalLimit cost.Price) stores {
	t.Helper()
	quiet := slog.New(slog.DiscardHandler)
	db, err := sqlite.Open(t.Context(), sqlite.Config{Path: t.TempDir() + "/limits.sqlite", Logger: quiet})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	flags, err := flagsqlitestore.Open(t.Context(), flagsqlitestore.Config{DB: db})
	if err != nil {
		t.Fatalf("open flags: %v", err)
	}
	costs, err := costsqlitestore.Open(t.Context(), costsqlitestore.Config{DB: db, Limit: globalLimit})
	if err != nil {
		t.Fatalf("open cost store: %v", err)
	}
	return stores{flags: flags, costs: costs, budgets: costsqlitestore.NewKeyedBudget(costs)}
}

func goodConfig(st stores) Config {
	return Config{
		Flags:             st.flags,
		Budgets:           st.budgets,
		Global:            st.costs,
		Auth:              StubOwnerAuth{},
		GuestMaxSessions:  testGuestMax,
		SessionMaxSeconds: testCapSeconds,
		DailySpendCents:   testDailyCents,
		OwnerDefaultLimit: testOwnerLimit,
	}
}

// TestSwitchAndCodesMatchBroker pins the shared contract with the mint
// path. The flag declaration and both refusal codes alias the broker
// values, so a rename on either side breaks here instead of splitting
// the switch from the refusal it causes.
func TestSwitchAndCodesMatchBroker(t *testing.T) {
	if SessionsPaused.Name != broker.KillSwitch.Name {
		t.Fatalf("switch name %q, broker reads %q", SessionsPaused.Name, broker.KillSwitch.Name)
	}
	if SessionsPaused.Name != "sessions_paused" {
		t.Fatalf("switch name %q, want sessions_paused", SessionsPaused.Name)
	}
	if CodeSessionsPaused != broker.CodePaused {
		t.Fatalf("paused code %q, broker refuses %q", CodeSessionsPaused, broker.CodePaused)
	}
	if CodeGuestLimit != broker.CodeQuota {
		t.Fatalf("guest code %q, broker refuses %q", CodeGuestLimit, broker.CodeQuota)
	}
}

// TestConfigValidation pins every refused Config. Each bad field fails
// with ErrInvalid and never with a dependency error.
func TestConfigValidation(t *testing.T) {
	st := openStores(t, testGlobalLimit)
	good := goodConfig(st)
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"nil flags", func(c *Config) { c.Flags = nil }},
		{"nil budgets", func(c *Config) { c.Budgets = nil }},
		{"nil global", func(c *Config) { c.Global = nil }},
		{"nil auth", func(c *Config) { c.Auth = nil }},
		{"zero guest cap", func(c *Config) { c.GuestMaxSessions = 0 }},
		{"short session cap", func(c *Config) { c.SessionMaxSeconds = 59 }},
		{"long session cap", func(c *Config) { c.SessionMaxSeconds = 10801 }},
		{"negative daily ceiling", func(c *Config) { c.DailySpendCents = -1 }},
		{"zero owner default", func(c *Config) { c.OwnerDefaultLimit = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := good
			tc.mutate(&cfg)
			svc, err := New(cfg)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("New = (%v, %v), want ErrInvalid", svc, err)
			}
			if svc != nil {
				t.Fatalf("New returned a service alongside the error")
			}
		})
	}
}

// TestSwitchFlipsWithoutRestart pins the kill switch. Flipping through
// the service reads back true on the same store the broker mints from,
// with no restart between the write and the read.
func TestSwitchFlipsWithoutRestart(t *testing.T) {
	st := openStores(t, testGlobalLimit)
	svc, err := New(goodConfig(st))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	paused, err := svc.Paused(t.Context())
	if err != nil {
		t.Fatalf("Paused: %v", err)
	}
	if paused {
		t.Fatalf("Paused = true before any flip")
	}
	if err := svc.SetPaused(t.Context(), true); err != nil {
		t.Fatalf("SetPaused: %v", err)
	}
	paused, err = svc.Paused(t.Context())
	if err != nil {
		t.Fatalf("Paused after flip: %v", err)
	}
	if !paused {
		t.Fatalf("Paused = false right after flipping it on")
	}
	raw, _, err := st.flags.Bool(t.Context(), broker.KillSwitch)
	if err != nil {
		t.Fatalf("read raw flag: %v", err)
	}
	if !raw {
		t.Fatalf("raw flag = false after the service flipped it on")
	}
	if err := svc.SetPaused(t.Context(), false); err != nil {
		t.Fatalf("SetPaused off: %v", err)
	}
	paused, err = svc.Paused(t.Context())
	if err != nil {
		t.Fatalf("Paused after unflip: %v", err)
	}
	if paused {
		t.Fatalf("Paused = true right after flipping it off")
	}
}

// TestGuestQuota pins both sides of the session count cap. Under the cap
// passes, at the cap refuses with ErrGuestLimit, and a negative count is
// a caller bug rather than a quota refusal.
func TestGuestQuota(t *testing.T) {
	st := openStores(t, testGlobalLimit)
	svc, err := New(goodConfig(st))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := svc.CheckGuestSessions(testGuestMax - 1); err != nil {
		t.Fatalf("CheckGuestSessions under cap: %v", err)
	}
	if err := svc.CheckGuestSessions(testGuestMax); !errors.Is(err, ErrGuestLimit) {
		t.Fatalf("CheckGuestSessions at cap = %v, want ErrGuestLimit", err)
	}
	if err := svc.CheckGuestSessions(testGuestMax + 3); !errors.Is(err, ErrGuestLimit) {
		t.Fatalf("CheckGuestSessions over cap = %v, want ErrGuestLimit", err)
	}
	if err := svc.CheckGuestSessions(-1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("CheckGuestSessions negative = %v, want ErrInvalid", err)
	}
}

// TestOwnerSpendMatchesSettledReservation is the pass pin for spend. A
// reserve plus a settle books real spend on the ledger, and the admin
// figure answers ceiling minus headroom with the settled amount spent.
func TestOwnerSpendMatchesSettledReservation(t *testing.T) {
	st := openStores(t, testGlobalLimit)
	svc, err := New(goodConfig(st))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := st.budgets.SetLimit(t.Context(), testOwner, testOwnerLimit); err != nil {
		t.Fatalf("SetLimit: %v", err)
	}
	reservation, err := st.budgets.Reserve(t.Context(), testOwner, testSettled)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := st.budgets.Settle(t.Context(), testOwner, reservation, testSettled); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	figure, err := svc.OwnerSpend(t.Context(), testOwner)
	if err != nil {
		t.Fatalf("OwnerSpend: %v", err)
	}
	if figure.CeilingND != int64(testOwnerLimit) {
		t.Fatalf("CeilingND = %d, want %d", figure.CeilingND, int64(testOwnerLimit))
	}
	if figure.SpentND != int64(testSettled) {
		t.Fatalf("SpentND = %d, want settled %d", figure.SpentND, int64(testSettled))
	}
	if figure.RemainingND != int64(testOwnerLimit-testSettled) {
		t.Fatalf("RemainingND = %d, want %d", figure.RemainingND, int64(testOwnerLimit-testSettled))
	}
}

// TestOwnerSpendUnknownOwner is the refusal beside the pass pin. An
// owner with no ceiling has not started a session, and the lookup says
// so with cost.ErrUnknownOwner rather than a zero figure.
func TestOwnerSpendUnknownOwner(t *testing.T) {
	st := openStores(t, testGlobalLimit)
	svc, err := New(goodConfig(st))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	figure, err := svc.OwnerSpend(t.Context(), testUnknownOwner)
	if !errors.Is(err, cost.ErrUnknownOwner) {
		t.Fatalf("OwnerSpend unknown = (%v, %v), want cost.ErrUnknownOwner", figure, err)
	}
	if _, err := svc.OwnerSpend(t.Context(), ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("OwnerSpend empty = %v, want ErrInvalid", err)
	}
}

// TestGlobalSpendTracksSettledReservations pins the headline figure. The
// ceiling and the headroom both read from the cost store, so a settled
// session moves the spent figure by exactly its actual price.
func TestGlobalSpendTracksSettledReservations(t *testing.T) {
	st := openStores(t, testGlobalLimit)
	svc, err := New(goodConfig(st))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	figure, err := svc.GlobalSpend(t.Context())
	if err != nil {
		t.Fatalf("GlobalSpend fresh: %v", err)
	}
	if figure.SpentND != 0 {
		t.Fatalf("SpentND fresh = %d, want 0", figure.SpentND)
	}
	if figure.CeilingND != int64(testGlobalLimit) {
		t.Fatalf("CeilingND = %d, want %d", figure.CeilingND, int64(testGlobalLimit))
	}
	if err := st.budgets.SetLimit(t.Context(), testOwner, testOwnerLimit); err != nil {
		t.Fatalf("SetLimit: %v", err)
	}
	reservation, err := st.budgets.Reserve(t.Context(), testOwner, testSettled)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := st.budgets.Settle(t.Context(), testOwner, reservation, testSettled); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	figure, err = svc.GlobalSpend(t.Context())
	if err != nil {
		t.Fatalf("GlobalSpend after settle: %v", err)
	}
	if figure.SpentND != int64(testSettled) {
		t.Fatalf("SpentND = %d, want settled %d", figure.SpentND, int64(testSettled))
	}
}

// TestSetOwnerLimitOverride pins the admin ceiling edit. Setting through
// the service changes the ceiling later figures subtract from, while the
// booked spend stays put.
func TestSetOwnerLimitOverride(t *testing.T) {
	st := openStores(t, testGlobalLimit)
	svc, err := New(goodConfig(st))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := st.budgets.SetLimit(t.Context(), testOwner, testOwnerLimit); err != nil {
		t.Fatalf("SetLimit: %v", err)
	}
	reservation, err := st.budgets.Reserve(t.Context(), testOwner, testSettled)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := st.budgets.Settle(t.Context(), testOwner, reservation, testSettled); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	doubled := testOwnerLimit * 2
	if err := svc.SetOwnerLimit(t.Context(), testOwner, doubled); err != nil {
		t.Fatalf("SetOwnerLimit: %v", err)
	}
	figure, err := svc.OwnerSpend(t.Context(), testOwner)
	if err != nil {
		t.Fatalf("OwnerSpend: %v", err)
	}
	if figure.CeilingND != int64(doubled) {
		t.Fatalf("CeilingND = %d, want %d", figure.CeilingND, int64(doubled))
	}
	if figure.SpentND != int64(testSettled) {
		t.Fatalf("SpentND = %d, want settled %d", figure.SpentND, int64(testSettled))
	}
	if err := svc.SetOwnerLimit(t.Context(), "", doubled); !errors.Is(err, ErrInvalid) {
		t.Fatalf("SetOwnerLimit empty = %v, want ErrInvalid", err)
	}
	if err := svc.SetOwnerLimit(t.Context(), testOwner, -1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("SetOwnerLimit negative = %v, want ErrInvalid", err)
	}
}
