package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"time"

	"github.com/nrynss/keel/gate"
	"github.com/nrynss/keel/sqlite"
)

// Send ceilings. Every ceiling refuses before the send, never after it,
// so a refused request leaves no row the sender could bill. The
// identical-response rule holds above this checker. A refused known
// address and a refused unknown address take the same tuple shape, and
// the route answers both alike.
const (
	// sendAddressBurst caps the codes one address may receive per
	// window. The rows carry every send, so the count reads the table
	// through the address hash index and survives a restart.
	sendAddressBurst = 5
	// sendAddressWindow is the sliding window the address cap counts.
	sendAddressWindow = 24 * time.Hour
	// sendClientBurst caps the code requests one client may make per
	// window. The client key comes from the shared gate, exactly as the
	// rest of the app keys clients, so a proxy header policy change
	// applies here with no second config.
	sendClientBurst = 10
	// sendClientWindow is the window the client cap sustains. The gate
	// restores one token per interval, so the interval is the window
	// divided by the burst.
	sendClientWindow = time.Hour
	// sendGlobalBurst caps the code mails the whole process may send
	// per window. It stays under the free sending tier. The database
	// window is the durable record, and the gate bucket beside it is
	// the same ceiling in token form, because the rule needs both
	// halves.
	sendGlobalBurst = 200
	// sendGlobalWindow is the sliding window the global cap counts.
	sendGlobalWindow = 24 * time.Hour
)

// sendLimitRuleName names the gate bucket family for code sends. One
// name keeps the client and global buckets of this route apart from
// every other route the process guards.
const sendLimitRuleName = "login-code"

// ErrSendLimited reports a code mail the ceilings refuse. The caller
// answers 429 with the Retry-After the checker returns, and sends
// nothing. Known and unknown addresses share this error, so the answer
// never reveals who registered.
var ErrSendLimited = errors.New("identity: code send limited")

// SendLimiter guards code mails with three ceilings. It is safe for
// concurrent use. Build one per process and consult it before every
// send. The route answers a refusal with 429 and the returned wait,
// known and unknown addresses alike.
type SendLimiter struct {
	db    *sqlite.DB
	now   func() time.Time
	probe http.Handler
}

// NewSendLimiter returns a limiter reading past sends from db and
// refill time from now. A nil now means the wall clock. The gate inside
// carries the client and global buckets, so a fake clock drives them in
// tests exactly as production time drives them.
func NewSendLimiter(db *sqlite.DB, now func() time.Time) (*SendLimiter, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: send limiter needs a database", ErrInvalid)
	}
	if now == nil {
		now = time.Now
	}
	inner, err := gate.New(gate.Config{Now: now})
	if err != nil {
		return nil, fmt.Errorf("identity: open send gate: %w", err)
	}
	probe, err := inner.Protect(gate.Rule{
		Name:      sendLimitRuleName,
		PerClient: gate.Limit{Burst: sendClientBurst, Every: sendClientWindow / sendClientBurst},
		Global:    gate.Limit{Burst: sendGlobalBurst, Every: sendGlobalWindow / sendGlobalBurst},
	}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	if err != nil {
		return nil, fmt.Errorf("identity: guard code sends: %w", err)
	}
	return &SendLimiter{db: db, now: now, probe: probe}, nil
}

// Allow reports whether one code mail may go out to addressHash. It
// returns ok true when the send may proceed. It returns ok false with
// the wait until a retry may succeed when a ceiling refuses. A refusal
// consumes no durable budget. The durable windows run first and the
// gate probe runs last, so a refused request spends no client token
// either. A refused address, known or not, takes the same shape, so
// the route answers both alike. A database fault or a missing request
// returns an error and ok false, and the caller must send nothing on
// any path but ok true.
func (l *SendLimiter) Allow(ctx context.Context, addressHash string, r *http.Request) (time.Duration, bool, error) {
	if addressHash == "" {
		return 0, false, fmt.Errorf("%w: send limiter needs an address hash", ErrInvalid)
	}
	if r == nil {
		return 0, false, fmt.Errorf("%w: send limiter needs the request", ErrInvalid)
	}
	now := l.now()
	cutoff := func(window time.Duration) int64 {
		return now.Add(-window).Unix()
	}
	if wait, limited, err := l.overWindow(ctx,
		"SELECT COUNT(*), MIN(created_at) FROM login_codes WHERE address_hash = ? AND created_at > ?",
		sendAddressBurst, sendAddressWindow, now, addressHash, cutoff(sendAddressWindow)); err != nil {
		return 0, false, err
	} else if limited {
		return wait, false, nil
	}
	if wait, limited, err := l.overWindow(ctx,
		"SELECT COUNT(*), MIN(created_at) FROM login_codes WHERE created_at > ?",
		sendGlobalBurst, sendGlobalWindow, now, cutoff(sendGlobalWindow)); err != nil {
		return 0, false, err
	} else if limited {
		return wait, false, nil
	}
	if wait, limited, err := l.overClient(r); err != nil {
		return 0, false, err
	} else if limited {
		return wait, false, nil
	}
	return 0, true, nil
}

// overWindow counts the rows one sliding window query finds. It reports
// limited true with the wait until the oldest row in the window ages
// out once the count reaches burst. An empty window allows. The wait
// never drops below one second, so a client never retries into the
// same refusal.
func (l *SendLimiter) overWindow(ctx context.Context, query string, burst int, window time.Duration, now time.Time, args ...any) (time.Duration, bool, error) {
	var count int64
	var oldest sql.NullInt64
	if err := l.db.Reader().QueryRowContext(ctx, query, args...).Scan(&count, &oldest); err != nil {
		return 0, false, fmt.Errorf("identity: count code sends: %w", err)
	}
	if count < int64(burst) {
		return 0, false, nil
	}
	wait := time.Unix(oldest.Int64, 0).Add(window).Sub(now)
	if !oldest.Valid || wait < time.Second {
		wait = time.Second
	}
	return wait, true, nil
}

// overClient draws one token from the shared gate for r. The gate keys
// the client exactly as the rest of the app does, so proxy trust stays
// in one place. It runs only after both durable windows pass, so a
// request the database refuses never reaches it. A refusal consumes
// nothing, so hammering on 429s cannot spend the route budget. The wait
// comes from the refusal the gate wrote, in whole seconds.
func (l *SendLimiter) overClient(r *http.Request) (time.Duration, bool, error) {
	rec := httptest.NewRecorder()
	l.probe.ServeHTTP(rec, r)
	switch rec.Code {
	case http.StatusNoContent:
		return 0, false, nil
	case http.StatusTooManyRequests:
		seconds, err := strconv.Atoi(rec.Header().Get("Retry-After"))
		if err != nil || seconds < 1 {
			seconds = 1
		}
		return time.Duration(seconds) * time.Second, true, nil
	default:
		return 0, false, fmt.Errorf("%w: send gate answered %d", ErrSendLimited, rec.Code)
	}
}

// sendLimiterByService holds one limiter per service. The code route
// reads its service entry on every request, so the process guards sends
// through one limiter with one set of gate buckets.
var (
	sendLimiterMu        sync.Mutex
	sendLimiterByService = map[*Service]*SendLimiter{}
)

// SetSendLimiter binds one limiter to this service. The code route
// consults it before every send. Tests bind a limiter on a fake clock.
// A nil limiter clears the binding and restores the default.
func (s *Service) SetSendLimiter(l *SendLimiter) {
	sendLimiterMu.Lock()
	defer sendLimiterMu.Unlock()
	if l == nil {
		delete(sendLimiterByService, s)
		return
	}
	sendLimiterByService[s] = l
}

// sendLimiter returns the limiter bound to this service, or the shared
// default built on its database with the wall clock. The boot binds
// none and takes the default. Tests bind one on a fake clock.
func (s *Service) sendLimiter() *SendLimiter {
	sendLimiterMu.Lock()
	defer sendLimiterMu.Unlock()
	if l, ok := sendLimiterByService[s]; ok {
		return l
	}
	l, err := NewSendLimiter(s.db, nil)
	if err != nil {
		return nil
	}
	sendLimiterByService[s] = l
	return l
}
