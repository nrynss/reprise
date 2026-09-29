package identity_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nrynss/reprise/internal/identity"
	"github.com/nrynss/reprise/internal/mail"
)

// sendRequest builds the request the limiter keys the client on. The
// peer address is the whole key here, because the test peer is no
// trusted proxy, exactly as a direct caller keys in production.
func sendRequest(remote string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/login/code", nil)
	req.RemoteAddr = remote
	return req
}

// sendIfAllowed mirrors the request order the route must keep.
// Check first, then send only on allow, so a refusal leaves the mail
// fake untouched.
func sendIfAllowed(t *testing.T, svc *identity.Service, limiter *identity.SendLimiter, sessionID, address, remote string) (time.Duration, bool) {
	t.Helper()
	wait, ok, err := limiter.Allow(t.Context(), hmacHex(address), sendRequest(remote))
	if err != nil {
		t.Fatalf("allow %q: %v", address, err)
	}
	if ok {
		if err := svc.RequestLoginCode(t.Context(), sessionID, address); err != nil {
			t.Fatalf("send %q: %v", address, err)
		}
	}
	return wait, ok
}

// openSendLimiter wires a service with its mail fake and a limiter on
// the same database and fake clock.
func openSendLimiter(t *testing.T, clock *testClock) (*identity.Service, *identity.SendLimiter, *mail.Fake, string) {
	t.Helper()
	svc, db, fake := openLogin(t, clock)
	limiter, err := identity.NewSendLimiter(db, clock.now)
	if err != nil {
		t.Fatalf("open send limiter: %v", err)
	}
	cookie, _ := loginGuest(t, svc)
	return svc, limiter, fake, sessionID(t, cookie.Value)
}

// TestSendLimiterRefusesSixthCodeToOneAddress drives the address
// ceiling to refusal with the fake clock, then past its window. The
// mail fake shows no send on the refused request.
func TestSendLimiterRefusesSixthCodeToOneAddress(t *testing.T) {
	t.Parallel()
	start := time.Unix(1758000000, 0)
	clock := &testClock{at: start}
	svc, limiter, fake, session := openSendLimiter(t, clock)

	for i := 0; i < 5; i++ {
		if _, ok := sendIfAllowed(t, svc, limiter, session, "capped@example.com", "203.0.113.7:4000"); !ok {
			t.Fatalf("send %d of 5 refused, want allow", i+1)
		}
	}
	wait, ok := sendIfAllowed(t, svc, limiter, session, "capped@example.com", "203.0.113.7:4000")
	if ok {
		t.Fatal("sixth code to one address allowed, want refusal")
	}
	if wait != 24*time.Hour {
		t.Fatalf("address retry wait = %v, want exactly 24h", wait)
	}
	if len(fake.Messages()) != 5 {
		t.Fatalf("mail fake holds %d messages, want 5 with none on refusal", len(fake.Messages()))
	}

	clock.at = start.Add(24*time.Hour + time.Second)
	if _, ok := sendIfAllowed(t, svc, limiter, session, "capped@example.com", "203.0.113.7:4000"); !ok {
		t.Fatal("send past the address window refused, want allow")
	}
	if len(fake.Messages()) != 6 {
		t.Fatalf("mail fake holds %d messages, want 6 past the window", len(fake.Messages()))
	}
}

// TestSendLimiterRefusesEleventhRequestFromOneClient drives the client
// ceiling to refusal, then past its refill. Each request names a fresh
// address, so only the client bucket can trip.
func TestSendLimiterRefusesEleventhRequestFromOneClient(t *testing.T) {
	t.Parallel()
	start := time.Unix(1758000000, 0)
	clock := &testClock{at: start}
	svc, limiter, fake, session := openSendLimiter(t, clock)

	for i := 0; i < 10; i++ {
		address := fmt.Sprintf("client-%d@example.com", i)
		if _, ok := sendIfAllowed(t, svc, limiter, session, address, "203.0.113.9:4000"); !ok {
			t.Fatalf("request %d of 10 refused, want allow", i+1)
		}
	}
	wait, ok := sendIfAllowed(t, svc, limiter, session, "client-10@example.com", "203.0.113.9:4000")
	if ok {
		t.Fatal("eleventh request from one client allowed, want refusal")
	}
	if wait != 6*time.Minute {
		t.Fatalf("client retry wait = %v, want exactly 6 minutes", wait)
	}
	if len(fake.Messages()) != 10 {
		t.Fatalf("mail fake holds %d messages, want 10 with none on refusal", len(fake.Messages()))
	}

	clock.at = start.Add(61 * time.Minute)
	if _, ok := sendIfAllowed(t, svc, limiter, session, "client-10@example.com", "203.0.113.9:4000"); !ok {
		t.Fatal("request past the client refill refused, want allow")
	}
	if len(fake.Messages()) != 11 {
		t.Fatalf("mail fake holds %d messages, want 11 past the refill", len(fake.Messages()))
	}
}

// TestSendLimiterRefusesPastDailyGlobal fills the daily ceiling with
// stored rows, then shows the next send refused with no mail, and
// allowed past the window.
func TestSendLimiterRefusesPastDailyGlobal(t *testing.T) {
	t.Parallel()
	start := time.Unix(1758000000, 0)
	clock := &testClock{at: start}
	svc, limiter, fake, session := openSendLimiter(t, clock)

	for i := 0; i < 200; i++ {
		address := fmt.Sprintf("crowd-%d@example.com", i)
		if err := svc.RequestLoginCode(t.Context(), session, address); err != nil {
			t.Fatalf("seed send %d: %v", i, err)
		}
	}
	if len(fake.Messages()) != 200 {
		t.Fatalf("mail fake holds %d messages, want the 200 seeds", len(fake.Messages()))
	}
	wait, ok := sendIfAllowed(t, svc, limiter, session, "crowd-extra@example.com", "203.0.113.11:4000")
	if ok {
		t.Fatal("send past the daily global allowed, want refusal")
	}
	if wait != 24*time.Hour {
		t.Fatalf("global retry wait = %v, want exactly 24h", wait)
	}
	if len(fake.Messages()) != 200 {
		t.Fatalf("mail fake holds %d messages, want 200 with none on refusal", len(fake.Messages()))
	}

	clock.at = start.Add(24*time.Hour + time.Second)
	if _, ok := sendIfAllowed(t, svc, limiter, session, "crowd-extra@example.com", "203.0.113.11:4000"); !ok {
		t.Fatal("send past the global window refused, want allow")
	}
	if len(fake.Messages()) != 201 {
		t.Fatalf("mail fake holds %d messages, want 201 past the window", len(fake.Messages()))
	}
}

// TestSendLimiterRefusesKnownAndUnknownAlike shows a refused known
// address and a refused unknown address take the same answer shape.
// Both return no error with ok false and a positive wait, so the route
// above can answer both alike and never reveal who registered.
func TestSendLimiterRefusesKnownAndUnknownAlike(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, limiter, fake, session := openSendLimiter(t, clock)

	for i := 0; i < 5; i++ {
		if _, ok := sendIfAllowed(t, svc, limiter, session, "known-capped@example.com", "203.0.113.13:4000"); !ok {
			t.Fatalf("known seed %d of 5 refused, want allow", i+1)
		}
	}
	knownWait, knownOK, err := limiter.Allow(t.Context(), hmacHex("known-capped@example.com"), sendRequest("203.0.113.13:4000"))
	if err != nil {
		t.Fatalf("known allow: %v", err)
	}

	for i := 0; i < 10; i++ {
		address := fmt.Sprintf("stranger-%d@example.com", i)
		if _, ok := sendIfAllowed(t, svc, limiter, session, address, "203.0.113.15:4000"); !ok {
			t.Fatalf("stranger seed %d of 10 refused, want allow", i+1)
		}
	}
	unknownWait, unknownOK, err := limiter.Allow(t.Context(), hmacHex("never-seen@example.com"), sendRequest("203.0.113.15:4000"))
	if err != nil {
		t.Fatalf("unknown allow: %v", err)
	}

	if knownOK || unknownOK {
		t.Fatalf("known ok = %v, unknown ok = %v, want both refused", knownOK, unknownOK)
	}
	if knownWait <= 0 || unknownWait <= 0 {
		t.Fatalf("known wait = %v, unknown wait = %v, want both positive", knownWait, unknownWait)
	}
	if len(fake.Messages()) != 15 {
		t.Fatalf("mail fake holds %d messages, want 15 with none on refusal", len(fake.Messages()))
	}
}

// TestNewSendLimiterRefusesNoDatabase checks the constructor names a
// missing database instead of returning a limiter that cannot count.
func TestNewSendLimiterRefusesNoDatabase(t *testing.T) {
	t.Parallel()
	if _, err := identity.NewSendLimiter(nil, nil); err == nil {
		t.Fatal("limiter with no database built, want an error")
	}
}
