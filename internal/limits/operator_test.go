// Pins for the operator check: guests get 401, signed-in callers with
// no listed identity get 403, and listed email and google identities
// reach the wrapped handler. The identity source is a stub map, so no
// sign-in flow runs here. The google case runs against that stub, and
// no ownership or seed row is touched on any path.
package limits

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nrynss/reprise/internal/identity"
)

// stubIdentities serves canned identity keys per user. A set error
// fails every lookup. seen records every user the check looked up, so
// tests prove guests trigger no lookup at all.
type stubIdentities struct {
	keys map[string][]OperatorIdentity
	err  error
	seen []string
}

func (s *stubIdentities) IdentitiesForUser(_ context.Context, userID string) ([]OperatorIdentity, error) {
	s.seen = append(s.seen, userID)
	if s.err != nil {
		return nil, s.err
	}
	return s.keys[userID], nil
}

// staticUsers returns a resolver that answers one fixed caller.
func staticUsers(userID, kind string, ok bool) RequestUsers {
	return func(_ *http.Request) (string, string, bool) {
		return userID, kind, ok
	}
}

// serveOperator runs one request through the check and reports the
// status, the raw body, and whether the wrapped handler ran.
func serveOperator(t *testing.T, users RequestUsers, operators []string, ids *stubIdentities) (int, []byte, bool) {
	t.Helper()
	auth, err := NewOperatorAuth(operators, users, ids)
	if err != nil {
		t.Fatalf("NewOperatorAuth: %v", err)
	}
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	request := httptest.NewRequest(http.MethodGet, PatternLimits, nil)
	recorder := httptest.NewRecorder()
	auth.Authorize(next).ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.Bytes(), called
}

// TestOperatorAuthGuestGets401 pins the anonymous refusal. With no
// resolved caller the check answers 401 under the operator code, runs
// no lookup, and never reaches the handler.
func TestOperatorAuthGuestGets401(t *testing.T) {
	ids := &stubIdentities{keys: map[string][]OperatorIdentity{}}
	status, body, called := serveOperator(t, staticUsers("", "", false), []string{"email:boss@example.com"}, ids)
	if status != http.StatusUnauthorized {
		t.Fatalf("guest status = %d, want 401", status)
	}
	if code := decodeCode(t, body); code != CodeOperatorRequired {
		t.Fatalf("guest code = %q, want %q", code, CodeOperatorRequired)
	}
	if called {
		t.Fatalf("guest request reached the wrapped handler")
	}
	if len(ids.seen) != 0 {
		t.Fatalf("guest request looked up %d users, want none", len(ids.seen))
	}
}

// TestOperatorAuthGuestKindGets401 pins the guest-user refusal. A
// resolved guest is still anonymous for the admin surface, so it gets
// the same 401 with no lookup.
func TestOperatorAuthGuestKindGets401(t *testing.T) {
	ids := &stubIdentities{keys: map[string][]OperatorIdentity{}}
	status, body, called := serveOperator(t, staticUsers("user-1", identity.KindGuest, true), []string{"email:boss@example.com"}, ids)
	if status != http.StatusUnauthorized {
		t.Fatalf("guest kind status = %d, want 401", status)
	}
	if code := decodeCode(t, body); code != CodeOperatorRequired {
		t.Fatalf("guest kind code = %q, want %q", code, CodeOperatorRequired)
	}
	if called {
		t.Fatalf("guest request reached the wrapped handler")
	}
	if len(ids.seen) != 0 {
		t.Fatalf("guest request looked up %d users, want none", len(ids.seen))
	}
}

// TestOperatorAuthNonOperatorGets403 pins the signed-in refusal. The
// caller holds an identity, but none is listed, so the check answers
// 403 under the operator code.
func TestOperatorAuthNonOperatorGets403(t *testing.T) {
	ids := &stubIdentities{keys: map[string][]OperatorIdentity{
		"user-1": {{Provider: "email", Subject: "visitor@example.com"}},
	}}
	status, body, called := serveOperator(t, staticUsers("user-1", identity.KindOwner, true),
		[]string{"email:boss@example.com"}, ids)
	if status != http.StatusForbidden {
		t.Fatalf("non-operator status = %d, want 403", status)
	}
	if code := decodeCode(t, body); code != CodeOperatorRequired {
		t.Fatalf("non-operator code = %q, want %q", code, CodeOperatorRequired)
	}
	if called {
		t.Fatalf("non-operator request reached the wrapped handler")
	}
	if len(ids.seen) != 1 || ids.seen[0] != "user-1" {
		t.Fatalf("lookup users = %q, want [user-1]", ids.seen)
	}
}

// TestOperatorAuthEmailOperatorPasses pins the email allow path. The
// entry matches case-insensitively with surrounding space, the way
// the settings file may carry it.
func TestOperatorAuthEmailOperatorPasses(t *testing.T) {
	ids := &stubIdentities{keys: map[string][]OperatorIdentity{
		"user-1": {{Provider: "email", Subject: "boss@example.com"}},
	}}
	status, _, called := serveOperator(t, staticUsers("user-1", identity.KindOwner, true),
		[]string{"  Email:BOSS@example.com "}, ids)
	if status != http.StatusOK {
		t.Fatalf("email operator status = %d, want 200", status)
	}
	if !called {
		t.Fatalf("email operator never reached the wrapped handler")
	}
}

// TestOperatorAuthGoogleOperatorPasses pins the google allow path
// against the stubbed identity. The issuer sub compares exactly.
func TestOperatorAuthGoogleOperatorPasses(t *testing.T) {
	ids := &stubIdentities{keys: map[string][]OperatorIdentity{
		"user-1": {{Provider: "google", Subject: "sub-123"}},
	}}
	status, _, called := serveOperator(t, staticUsers("user-1", identity.KindOwner, true),
		[]string{"google:sub-123"}, ids)
	if status != http.StatusOK {
		t.Fatalf("google operator status = %d, want 200", status)
	}
	if !called {
		t.Fatalf("google operator never reached the wrapped handler")
	}
}

// TestOperatorAuthUnknownPrefixNeverMatches pins the closed provider
// set. An entry outside email and google grants nothing, even when a
// held identity names the same prefix.
func TestOperatorAuthUnknownPrefixNeverMatches(t *testing.T) {
	ids := &stubIdentities{keys: map[string][]OperatorIdentity{
		"user-1": {{Provider: "sms", Subject: "+123"}},
	}}
	status, body, called := serveOperator(t, staticUsers("user-1", identity.KindOwner, true),
		[]string{"sms:+123"}, ids)
	if status != http.StatusForbidden {
		t.Fatalf("unknown prefix status = %d, want 403", status)
	}
	if code := decodeCode(t, body); code != CodeOperatorRequired {
		t.Fatalf("unknown prefix code = %q, want %q", code, CodeOperatorRequired)
	}
	if called {
		t.Fatalf("unknown prefix request reached the wrapped handler")
	}
}

// TestOperatorAuthEmptyListDeniesSignedIn pins the empty setting. With
// nobody listed, a signed-in caller still gets 403.
func TestOperatorAuthEmptyListDeniesSignedIn(t *testing.T) {
	ids := &stubIdentities{keys: map[string][]OperatorIdentity{
		"user-1": {{Provider: "email", Subject: "boss@example.com"}},
	}}
	status, _, called := serveOperator(t, staticUsers("user-1", identity.KindOwner, true), nil, ids)
	if status != http.StatusForbidden {
		t.Fatalf("empty list status = %d, want 403", status)
	}
	if called {
		t.Fatalf("empty list request reached the wrapped handler")
	}
}

// TestOperatorAuthLookupFailureIs500 pins the broken source. A lookup
// fault answers 500 under the internal code instead of refusing as a
// non-operator.
func TestOperatorAuthLookupFailureIs500(t *testing.T) {
	ids := &stubIdentities{err: errors.New("stub lookup failed")}
	status, body, called := serveOperator(t, staticUsers("user-1", identity.KindOwner, true),
		[]string{"email:boss@example.com"}, ids)
	if status != http.StatusInternalServerError {
		t.Fatalf("lookup failure status = %d, want 500", status)
	}
	if code := decodeCode(t, body); code != CodeInternal {
		t.Fatalf("lookup failure code = %q, want %q", code, CodeInternal)
	}
	if called {
		t.Fatalf("failed lookup reached the wrapped handler")
	}
}

// TestNewOperatorAuthValidatesSeams pins construction. A nil source
// fails with ErrInvalid and no service, while a nil resolver defaults
// to the middleware reader.
func TestNewOperatorAuthValidatesSeams(t *testing.T) {
	ids := &stubIdentities{}
	if auth, err := NewOperatorAuth(nil, staticUsers("u", identity.KindOwner, true), nil); err == nil || auth != nil {
		t.Fatalf("nil source = (%v, %v), want ErrInvalid", auth, err)
	} else if !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil source error = %v, want ErrInvalid", err)
	}
	auth, err := NewOperatorAuth(nil, nil, ids)
	if err != nil {
		t.Fatalf("nil resolver with source: %v", err)
	}
	if auth == nil {
		t.Fatalf("nil resolver returned no check")
	}
	request := httptest.NewRequest(http.MethodGet, PatternLimits, nil)
	recorder := httptest.NewRecorder()
	called := false
	auth.Authorize(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("default resolver status = %d, want 401", recorder.Code)
	}
	if called {
		t.Fatalf("anonymous request reached the wrapped handler")
	}
}
