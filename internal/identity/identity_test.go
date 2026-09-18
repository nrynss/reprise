package identity_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nrynss/keel/id"
	"github.com/nrynss/keel/mediastore"
	mediastoreindex "github.com/nrynss/keel/mediastore/sqlitestore"
	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/identity"
	"github.com/nrynss/reprise/internal/store"
)

// testClock is a fixed clock the test moves by hand. Every stamp the
// package writes comes from it, so no check reads the wall clock.
type testClock struct {
	at time.Time
}

// now returns the clock time.
func (c *testClock) now() time.Time { return c.at }

// fixture opens a database with the diary schema and the session rows,
// then returns a service on the test clock.
func fixture(t *testing.T, clock *testClock) (*identity.Service, *sqlite.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "diary.db")
	db, err := sqlite.Open(t.Context(), sqlite.Config{
		Path:   path,
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := store.Open(t.Context(), db); err != nil {
		t.Fatalf("open diary store: %v", err)
	}
	svc, err := identity.New(t.Context(), identity.Config{
		DB:         db,
		SigningKey: "test-signing-key-with-enough-length",
		Now:        clock.now,
	})
	if err != nil {
		t.Fatalf("open identity service: %v", err)
	}
	return svc, db
}

// visit runs one request through the middleware and returns the response
// with the request user the handler saw.
func visit(svc *identity.Service, cookie *http.Cookie, handler http.Handler) (*httptest.ResponseRecorder, identity.User, bool) {
	var got identity.User
	var ok bool
	wrapped := svc.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok = identity.UserFromContext(r.Context())
		handler.ServeHTTP(w, r)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	return rec, got, ok
}

// sessionCookie returns the session cookie a response set.
func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == identity.CookieName {
			return c
		}
	}
	t.Fatalf("response set no %q cookie", identity.CookieName)
	return nil
}

// sessionID splits a cookie value into its session id.
func sessionID(t *testing.T, value string) string {
	t.Helper()
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		t.Fatalf("cookie value %q has %d parts, want id plus signature", value, len(parts))
	}
	return parts[0]
}

// seedEpisode writes one private episode owned by owner.
func seedEpisode(t *testing.T, db *sqlite.DB, episodeID, owner string) {
	t.Helper()
	if _, err := db.Writer().ExecContext(t.Context(), `INSERT INTO episodes
		(id, owner_id, number, title, state, visibility, share_token, seeded)
		VALUES (?, ?, 1, 'First', 'draft', 'private', 'share-1', 0)`, episodeID, owner); err != nil {
		t.Fatalf("seed episode: %v", err)
	}
}

// episodeReader serves 200 only when the request user owns the episode.
// It loads the owner from the database on every request, the way a real
// handler loads its episode before it checks ownership.
func episodeReader(svc *identity.Service, db *sqlite.DB, episodeID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var owner string
		if err := db.Reader().QueryRowContext(r.Context(), "SELECT owner_id FROM episodes WHERE id = ?", episodeID).Scan(&owner); err != nil {
			http.NotFound(w, r)
			return
		}
		if !svc.Owns(r.Context(), owner) {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("episode"))
	})
}

// rowCount counts rows in a table over the read pool.
func rowCount(t *testing.T, db *sqlite.DB, table string) int {
	t.Helper()
	var n int
	if err := db.Reader().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, n)
	}
	return n
}

// TestNewRefusesBadConfig checks the service refuses a nil database and
// an empty signing key, so neither boots into an unsigned session.
func TestNewRefusesBadConfig(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	if _, err := identity.New(t.Context(), identity.Config{DB: nil, SigningKey: "key", Now: clock.now}); err == nil {
		t.Fatal("New with nil database succeeded, want refusal")
	}
	path := filepath.Join(t.TempDir(), "diary.db")
	db, err := sqlite.Open(t.Context(), sqlite.Config{Path: path, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := identity.New(t.Context(), identity.Config{DB: db, SigningKey: "", Now: clock.now}); err == nil {
		t.Fatal("New with empty signing key succeeded, want refusal")
	}
}

// TestFirstVisitMintsGuestAndSession checks a cookieless visit creates one
// guest user and one session row, and answers with a signed cookie that
// carries only the session id.
func TestFirstVisitMintsGuestAndSession(t *testing.T) {
	t.Parallel()
	start := time.Unix(1758000000, 0)
	clock := &testClock{at: start}
	svc, db := fixture(t, clock)
	quiet := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	rec, got, ok := visit(svc, nil, quiet)
	if rec.Code != http.StatusOK {
		t.Fatalf("first visit status = %d, want 200", rec.Code)
	}
	if !ok {
		t.Fatal("middleware set no user in context")
	}
	if rowCount(t, db, "users") != 1 {
		t.Fatalf("users holds %d rows, want the one guest", rowCount(t, db, "users"))
	}
	if rowCount(t, db, "guest_sessions") != 1 {
		t.Fatalf("guest_sessions holds %d rows, want the one session", rowCount(t, db, "guest_sessions"))
	}
	var kind string
	var created, seen int64
	if err := db.Reader().QueryRowContext(t.Context(), "SELECT kind, created_at, last_seen_at FROM users WHERE id = ?", got.ID).Scan(&kind, &created, &seen); err != nil {
		t.Fatalf("read guest row: %v", err)
	}
	if kind != identity.KindGuest {
		t.Fatalf("guest kind = %q, want guest", kind)
	}
	if created != start.Unix() || seen != start.Unix() {
		t.Fatalf("guest stamps = %d/%d, want both %d", created, seen, start.Unix())
	}
	cookie := sessionCookie(t, rec)
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
		t.Fatalf("cookie flags = HttpOnly:%v Secure:%v SameSite:%v Path:%q, want true true Lax /",
			cookie.HttpOnly, cookie.Secure, cookie.SameSite, cookie.Path)
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 || !id.Valid(parts[0]) {
		t.Fatalf("cookie value %q is not a signed session id", cookie.Value)
	}
	var sessionUser string
	if err := db.Reader().QueryRowContext(t.Context(), "SELECT user_id FROM guest_sessions WHERE id = ?", parts[0]).Scan(&sessionUser); err != nil {
		t.Fatalf("read session row: %v", err)
	}
	if sessionUser != got.ID {
		t.Fatal("session row points at a different user than the request saw")
	}
	if strings.Contains(cookie.Value, got.ID) {
		t.Fatal("cookie value carries the user id, want only the session id")
	}
}

// TestCookieReadsOwnEpisode checks the minted cookie passes an ownership
// check for its own episode.
func TestCookieReadsOwnEpisode(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, db := fixture(t, clock)
	quiet := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	first, guest, _ := visit(svc, nil, quiet)
	seedEpisode(t, db, "ep-1", guest.ID)
	rec, _, _ := visit(svc, sessionCookie(t, first), episodeReader(svc, db, "ep-1"))
	if rec.Code != http.StatusOK || rec.Body.String() != "episode" {
		t.Fatalf("own episode status = %d body = %q, want 200 episode", rec.Code, rec.Body.String())
	}
}

// TestMediaServesOwnerWithRange checks the media store serves the owner
// full bodies and range slices through the authorizer, with no cached
// copy in between.
func TestMediaServesOwnerWithRange(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, db := fixture(t, clock)
	quiet := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	first, guest, _ := visit(svc, nil, quiet)
	cookie := sessionCookie(t, first)

	index, err := mediastoreindex.Open(t.Context(), mediastoreindex.Config{DB: db})
	if err != nil {
		t.Fatalf("open media index: %v", err)
	}
	media, err := mediastore.Open(t.Context(), mediastore.Config{
		Dir:       t.TempDir(),
		Index:     index,
		Authorize: svc.AuthorizeMedia,
	})
	if err != nil {
		t.Fatalf("open media store: %v", err)
	}
	body := "0123456789abcdef"
	blobID, err := media.Persist(t.Context(), bytes.NewReader([]byte(body)), mediastore.Put{
		ContentType: "audio/mpeg",
		Owner:       guest.ID,
	})
	if err != nil {
		t.Fatalf("persist blob: %v", err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /media/{id}", media)

	full := httptest.NewRequest(http.MethodGet, "/media/"+blobID, nil)
	full.AddCookie(cookie)
	fullRec := httptest.NewRecorder()
	mux.ServeHTTP(fullRec, full)
	if fullRec.Code != http.StatusOK || fullRec.Body.String() != body {
		t.Fatalf("own media status = %d body = %q, want 200 and the bytes", fullRec.Code, fullRec.Body.String())
	}
	if cache := fullRec.Header().Get("Cache-Control"); cache != "private, no-store" {
		t.Fatalf("own media Cache-Control = %q, want private no-store", cache)
	}

	slice := httptest.NewRequest(http.MethodGet, "/media/"+blobID, nil)
	slice.AddCookie(cookie)
	slice.Header.Set("Range", "bytes=0-3")
	sliceRec := httptest.NewRecorder()
	mux.ServeHTTP(sliceRec, slice)
	if sliceRec.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d, want 206", sliceRec.Code)
	}
	if sliceRec.Body.String() != "0123" {
		t.Fatalf("range body = %q, want the first four bytes", sliceRec.Body.String())
	}
	if want := "bytes 0-3/16"; sliceRec.Header().Get("Content-Range") != want {
		t.Fatalf("Content-Range = %q, want %q", sliceRec.Header().Get("Content-Range"), want)
	}
}

// TestAnonymousGets404 checks a request without a cookie reads neither
// another episode nor its media, even though the middleware mints it a
// fresh guest on the way through.
func TestAnonymousGets404(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, db := fixture(t, clock)
	quiet := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	_, owner, _ := visit(svc, nil, quiet)
	seedEpisode(t, db, "ep-1", owner.ID)

	otherVisit, _, _ := visit(svc, nil, quiet)
	otherCookie := sessionCookie(t, otherVisit)
	rec, _, _ := visit(svc, otherCookie, episodeReader(svc, db, "ep-1"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("other guest episode status = %d, want 404", rec.Code)
	}
	bare, _, _ := visit(svc, nil, episodeReader(svc, db, "ep-1"))
	if bare.Code != http.StatusNotFound {
		t.Fatalf("cookieless episode status = %d, want 404", bare.Code)
	}

	index, err := mediastoreindex.Open(t.Context(), mediastoreindex.Config{DB: db})
	if err != nil {
		t.Fatalf("open media index: %v", err)
	}
	media, err := mediastore.Open(t.Context(), mediastore.Config{
		Dir:       t.TempDir(),
		Index:     index,
		Authorize: svc.AuthorizeMedia,
	})
	if err != nil {
		t.Fatalf("open media store: %v", err)
	}
	blobID, err := media.Persist(t.Context(), bytes.NewReader([]byte("0123456789abcdef")), mediastore.Put{
		ContentType: "audio/mpeg",
		Owner:       owner.ID,
	})
	if err != nil {
		t.Fatalf("persist blob: %v", err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /media/{id}", media)
	anon := httptest.NewRequest(http.MethodGet, "/media/"+blobID, nil)
	anonRec := httptest.NewRecorder()
	mux.ServeHTTP(anonRec, anon)
	if anonRec.Code != http.StatusNotFound {
		t.Fatalf("cookieless media status = %d, want 404", anonRec.Code)
	}
}

// TestRevokedSessionFailsNextRequest checks revocation closes both the
// episode read and the media read on the very next request.
func TestRevokedSessionFailsNextRequest(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, db := fixture(t, clock)
	quiet := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	first, guest, _ := visit(svc, nil, quiet)
	cookie := sessionCookie(t, first)
	seedEpisode(t, db, "ep-1", guest.ID)

	before, _, _ := visit(svc, cookie, episodeReader(svc, db, "ep-1"))
	if before.Code != http.StatusOK {
		t.Fatalf("pre-revoke episode status = %d, want 200", before.Code)
	}
	if err := svc.Revoke(t.Context(), sessionID(t, cookie.Value)); err != nil {
		t.Fatalf("revoke session: %v", err)
	}
	after, _, _ := visit(svc, cookie, episodeReader(svc, db, "ep-1"))
	if after.Code != http.StatusNotFound {
		t.Fatalf("post-revoke episode status = %d, want 404", after.Code)
	}

	index, err := mediastoreindex.Open(t.Context(), mediastoreindex.Config{DB: db})
	if err != nil {
		t.Fatalf("open media index: %v", err)
	}
	media, err := mediastore.Open(t.Context(), mediastore.Config{
		Dir:       t.TempDir(),
		Index:     index,
		Authorize: svc.AuthorizeMedia,
	})
	if err != nil {
		t.Fatalf("open media store: %v", err)
	}
	blobID, err := media.Persist(t.Context(), bytes.NewReader([]byte("0123456789abcdef")), mediastore.Put{
		ContentType: "audio/mpeg",
		Owner:       guest.ID,
	})
	if err != nil {
		t.Fatalf("persist blob: %v", err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /media/{id}", media)
	stale := httptest.NewRequest(http.MethodGet, "/media/"+blobID, nil)
	stale.AddCookie(cookie)
	staleRec := httptest.NewRecorder()
	mux.ServeHTTP(staleRec, stale)
	if staleRec.Code != http.StatusNotFound {
		t.Fatalf("post-revoke media status = %d, want 404", staleRec.Code)
	}
}

// TestForgedAndUnknownCookiesFail checks a tampered signature and a
// deleted session row both read as anonymous.
func TestForgedAndUnknownCookiesFail(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, db := fixture(t, clock)
	quiet := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	first, guest, _ := visit(svc, nil, quiet)
	cookie := sessionCookie(t, first)
	seedEpisode(t, db, "ep-1", guest.ID)
	reader := episodeReader(svc, db, "ep-1")

	forged := *cookie
	last := forged.Value[len(forged.Value)-1]
	if last == 'a' {
		forged.Value = forged.Value[:len(forged.Value)-1] + "b"
	} else {
		forged.Value = forged.Value[:len(forged.Value)-1] + "a"
	}
	rec, _, _ := visit(svc, &forged, reader)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("forged cookie episode status = %d, want 404", rec.Code)
	}

	if _, err := db.Writer().ExecContext(t.Context(), "DELETE FROM guest_sessions WHERE id = ?", sessionID(t, cookie.Value)); err != nil {
		t.Fatalf("delete session row: %v", err)
	}
	gone, _, _ := visit(svc, cookie, reader)
	if gone.Code != http.StatusNotFound {
		t.Fatalf("deleted session episode status = %d, want 404", gone.Code)
	}
}

// TestResolveTouchesLastSeen checks a valid cookie moves the last seen
// time to the current clock reading.
func TestResolveTouchesLastSeen(t *testing.T) {
	t.Parallel()
	start := time.Unix(1758000000, 0)
	clock := &testClock{at: start}
	svc, db := fixture(t, clock)
	quiet := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	first, guest, _ := visit(svc, nil, quiet)
	cookie := sessionCookie(t, first)

	clock.at = start.Add(time.Hour)
	rec, _, _ := visit(svc, cookie, quiet)
	if rec.Code != http.StatusOK {
		t.Fatalf("second visit status = %d, want 200", rec.Code)
	}
	var seen int64
	if err := db.Reader().QueryRowContext(t.Context(), "SELECT last_seen_at FROM users WHERE id = ?", guest.ID).Scan(&seen); err != nil {
		t.Fatalf("read last seen: %v", err)
	}
	if seen != clock.at.Unix() {
		t.Fatalf("last seen = %d, want %d", seen, clock.at.Unix())
	}
}

// TestOpenTwiceMigratesCleanly checks opening the service again changes
// nothing, so a second boot reuses the session rows.
func TestOpenTwiceMigratesCleanly(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, db := fixture(t, clock)
	quiet := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	if _, _, _ = visit(svc, nil, quiet); rowCount(t, db, "guest_sessions") != 1 {
		t.Fatal("want one session before reopen")
	}
	if _, err := identity.New(t.Context(), identity.Config{DB: db, SigningKey: "test-signing-key-with-enough-length", Now: clock.now}); err != nil {
		t.Fatalf("reopen identity service: %v", err)
	}
	if rowCount(t, db, "guest_sessions") != 1 {
		t.Fatal("reopen changed the session rows")
	}
}

// TestPublicBlobPassesAuthorizer checks a public blob needs no session,
// so the authorizer never blocks what the store already serves openly.
func TestPublicBlobPassesAuthorizer(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, _ := fixture(t, clock)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if !svc.AuthorizeMedia(req, mediastore.Blob{ID: "blob-1", Visibility: mediastore.Public}) {
		t.Fatal("authorizer refused a public blob")
	}
	if svc.AuthorizeMedia(req, mediastore.Blob{ID: "blob-2", Owner: "someone"}) {
		t.Fatal("authorizer served a private blob to an anonymous request")
	}
}
