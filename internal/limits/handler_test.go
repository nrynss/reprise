// Pins for the admin endpoints: the stub denies everything, the switch
// flips through HTTP at once, and the spend body matches the settled
// reservation the ledger holds.
package limits

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// allowAuth lets every admin call through. Tests use it to pin the
// endpoints themselves. Production passes the stub until the owner login
// lands.
type allowAuth struct{}

func (allowAuth) Authorize(next http.Handler) http.Handler { return next }

func testService(t *testing.T, auth OwnerAuth) (*Service, stores) {
	t.Helper()
	st := openStores(t, testGlobalLimit)
	cfg := goodConfig(st)
	cfg.Auth = auth
	svc, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc, st
}

func decodeCode(t *testing.T, body []byte) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	return envelope.Error.Code
}

// TestStubDeniesEveryEndpoint pins the owner seam. Until the login lands,
// the stub answers every admin call with the missing owner proof, and no
// caps, switch, or spend figure leaks through.
func TestStubDeniesEveryEndpoint(t *testing.T) {
	svc, _ := testService(t, StubOwnerAuth{})
	handler := svc.Handler()
	calls := []struct {
		method string
		target string
		body   string
	}{
		{http.MethodGet, PatternLimits, ""},
		{http.MethodPost, PatternPause, `{"paused":true}`},
		{http.MethodPost, PatternOwnerLimit, `{"owner":"x","limit_nd":1}`},
	}
	for _, call := range calls {
		var reader *strings.Reader
		if call.body == "" {
			reader = strings.NewReader("")
		} else {
			reader = strings.NewReader(call.body)
		}
		request := httptest.NewRequest(call.method, call.target, reader)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s %s status = %d, want 403", call.method, call.target, recorder.Code)
		}
		code := decodeCode(t, recorder.Body.Bytes())
		if code != CodeOwnerRequired {
			t.Fatalf("%s %s code = %q, want %q", call.method, call.target, code, CodeOwnerRequired)
		}
	}
	paused, err := svc.Paused(t.Context())
	if err != nil {
		t.Fatalf("Paused: %v", err)
	}
	if paused {
		t.Fatalf("denied pause call flipped the switch")
	}
}

// TestLimitsSnapshotServesCapsAndSpend pins the admin read. The body
// carries the caps from settings and the global spend from the ledger,
// with the switch off on a fresh store.
func TestLimitsSnapshotServesCapsAndSpend(t *testing.T) {
	svc, _ := testService(t, allowAuth{})
	request := httptest.NewRequest(http.MethodGet, PatternLimits, nil)
	recorder := httptest.NewRecorder()
	svc.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET limits status = %d, want 200", recorder.Code)
	}
	var snapshot LimitsSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot.SessionsPaused {
		t.Fatalf("snapshot shows the switch on before any flip")
	}
	if snapshot.Caps.GuestMaxSessions != testGuestMax {
		t.Fatalf("guest cap = %d, want %d", snapshot.Caps.GuestMaxSessions, testGuestMax)
	}
	if snapshot.Caps.SessionMaxSeconds != testCapSeconds {
		t.Fatalf("session cap = %d, want %d", snapshot.Caps.SessionMaxSeconds, testCapSeconds)
	}
	if snapshot.Caps.DailySpendCents != testDailyCents {
		t.Fatalf("daily ceiling = %d, want %d", snapshot.Caps.DailySpendCents, testDailyCents)
	}
	if snapshot.Global.CeilingND != int64(testGlobalLimit) {
		t.Fatalf("global ceiling = %d, want %d", snapshot.Global.CeilingND, int64(testGlobalLimit))
	}
	if snapshot.Global.SpentND != 0 {
		t.Fatalf("global spent = %d, want 0", snapshot.Global.SpentND)
	}
	if snapshot.Owner != nil {
		t.Fatalf("snapshot carries an owner figure nobody asked for")
	}
}

// TestPauseEndpointFlipsAtOnce pins the kill switch over HTTP. Posting
// paused reads back true on the next read, which is the same immediacy
// the next mint sees.
func TestPauseEndpointFlipsAtOnce(t *testing.T) {
	svc, _ := testService(t, allowAuth{})
	handler := svc.Handler()
	post := func(paused bool) PauseRequest {
		request := httptest.NewRequest(http.MethodPost, PatternPause, strings.NewReader(
			`{"paused":`+map[bool]string{true: "true", false: "false"}[paused]+`}`))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("POST pause status = %d, want 200", recorder.Code)
		}
		var answer PauseRequest
		if err := json.Unmarshal(recorder.Body.Bytes(), &answer); err != nil {
			t.Fatalf("decode pause answer: %v", err)
		}
		return answer
	}
	if answer := post(true); !answer.Paused {
		t.Fatalf("pause answer = false right after pausing")
	}
	request := httptest.NewRequest(http.MethodGet, PatternLimits, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var snapshot LimitsSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if !snapshot.SessionsPaused {
		t.Fatalf("snapshot shows the switch off right after pausing")
	}
	if answer := post(false); answer.Paused {
		t.Fatalf("pause answer = true right after unpausing")
	}
	bad := httptest.NewRequest(http.MethodPost, PatternPause, strings.NewReader(`{`))
	badRecorder := httptest.NewRecorder()
	handler.ServeHTTP(badRecorder, bad)
	if badRecorder.Code != http.StatusBadRequest {
		t.Fatalf("POST pause broken body status = %d, want 400", badRecorder.Code)
	}
	get := httptest.NewRequest(http.MethodGet, PatternPause, nil)
	getRecorder := httptest.NewRecorder()
	handler.ServeHTTP(getRecorder, get)
	if getRecorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET pause status = %d, want 405", getRecorder.Code)
	}
}

// TestOwnerEndpointSetsAndReadsSpend pins the per-owner round trip. A
// settled reservation moves the owner's figure, the lookup names it, and
// an unknown owner refuses instead of printing zeros.
func TestOwnerEndpointSetsAndReadsSpend(t *testing.T) {
	svc, st := testService(t, allowAuth{})
	handler := svc.Handler()
	setBody := `{"owner":"` + testOwner + `","limit_nd":` + strconv.FormatInt(int64(testOwnerLimit), 10) + `}`
	request := httptest.NewRequest(http.MethodPost, PatternOwnerLimit, strings.NewReader(setBody))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("POST owner status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	reservation, err := st.budgets.Reserve(t.Context(), testOwner, testSettled)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := st.budgets.Settle(t.Context(), testOwner, reservation, testSettled); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	lookup := httptest.NewRequest(http.MethodGet, PatternLimits+"?owner="+testOwner, nil)
	lookupRecorder := httptest.NewRecorder()
	handler.ServeHTTP(lookupRecorder, lookup)
	if lookupRecorder.Code != http.StatusOK {
		t.Fatalf("GET limits owner status = %d, want 200", lookupRecorder.Code)
	}
	var snapshot LimitsSnapshot
	if err := json.Unmarshal(lookupRecorder.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot.Owner == nil {
		t.Fatalf("snapshot carries no owner figure after asking for one")
	}
	if snapshot.Owner.SpentND != int64(testSettled) {
		t.Fatalf("owner spent = %d, want settled %d", snapshot.Owner.SpentND, int64(testSettled))
	}
	unknown := httptest.NewRequest(http.MethodGet, PatternLimits+"?owner="+testUnknownOwner, nil)
	unknownRecorder := httptest.NewRecorder()
	handler.ServeHTTP(unknownRecorder, unknown)
	if unknownRecorder.Code != http.StatusNotFound {
		t.Fatalf("GET limits unknown owner status = %d, want 404", unknownRecorder.Code)
	}
	code := decodeCode(t, unknownRecorder.Body.Bytes())
	if code != CodeUnknownOwner {
		t.Fatalf("unknown owner code = %q, want %q", code, CodeUnknownOwner)
	}
	empty := httptest.NewRequest(http.MethodPost, PatternOwnerLimit, strings.NewReader(`{"owner":"","limit_nd":1}`))
	emptyRecorder := httptest.NewRecorder()
	handler.ServeHTTP(emptyRecorder, empty)
	if emptyRecorder.Code != http.StatusBadRequest {
		t.Fatalf("POST owner empty status = %d, want 400", emptyRecorder.Code)
	}
}
