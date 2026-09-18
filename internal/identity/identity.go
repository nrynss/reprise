// Package identity carries guest sessions for the diary.
//
// A first visit creates a guest user row and a server side session row in
// the shared SQLite file. The browser holds a signed cookie that carries
// only the session id. Every request resolves the cookie against the rows,
// so revoking a session takes effect on the next request. Diary handlers
// check the resolved user owns the rows they read, and the media store
// applies the same check through AuthorizeMedia.
package identity

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/nrynss/keel/id"
	"github.com/nrynss/keel/mediastore"
	"github.com/nrynss/keel/sqlite"
)

// CookieName is the session cookie the browser holds. Its value carries
// only the session id and its signature.
const CookieName = "reprise_session"

// KindGuest marks a user row created on a first visit.
const KindGuest = "guest"

// KindOwner marks a user row with a login. No login exists yet, so this
// package only writes guest rows. The constant names the other value the
// kind column already holds.
const KindOwner = "owner"

// sessionLifetime is how long the browser keeps the session cookie. A
// returning guest stays on one user row across restarts, while the server
// rows remain revocable and erasable at any time.
const sessionLifetime = 180 * 24 * time.Hour

// schemaNamespace is the migration ledger namespace this package owns. It
// shares the database file with the diary schema without colliding,
// because each namespace keeps its own ledger.
const schemaNamespace = "reprise_identity"

//go:embed migrations/*.sql
var migrations embed.FS

// ErrInvalid reports a Config this package cannot honour, such as a nil
// database or an empty signing key.
var ErrInvalid = errors.New("identity: invalid config")

// ErrNoSession reports a request with no usable guest session. The cookie
// is absent, malformed, badly signed, unknown or revoked.
var ErrNoSession = errors.New("identity: no session")

// Config configures a Service. DB is the shared handle. SigningKey is the
// resolved session signing secret. Now stamps created and seen times, and
// nil means time.Now.
type Config struct {
	DB         *sqlite.DB
	SigningKey string
	Now        func() time.Time
}

// User is one guest or owner row. Handlers compare its ID against the
// owner of the rows they read.
type User struct {
	ID        string
	Kind      string
	CreatedAt time.Time
	LastSeen  time.Time
}

// Service resolves guest sessions against the shared database. Create it
// with New, because the zero value has no database and no key. A Service
// is safe for concurrent use.
type Service struct {
	db  *sqlite.DB
	key []byte
	now func() time.Time
}

// New migrates the session rows and returns the Service. Open the diary
// store first, because the session rows reference its users table.
func New(ctx context.Context, cfg Config) (*Service, error) {
	if cfg.DB == nil {
		return nil, fmt.Errorf("%w: database must not be nil", ErrInvalid)
	}
	if cfg.SigningKey == "" {
		return nil, fmt.Errorf("%w: signing key must not be empty", ErrInvalid)
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	schema, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("identity: open: %w", err)
	}
	if err := sqlite.Migrate(ctx, cfg.DB, schemaNamespace, schema); err != nil {
		return nil, fmt.Errorf("identity: open: %w", err)
	}
	return &Service{db: cfg.DB, key: []byte(cfg.SigningKey), now: now}, nil
}

// Resolve returns the user behind the request cookie without minting. It
// returns ErrNoSession when the cookie is absent, malformed, badly
// signed, unknown or revoked. A valid session touches the last seen time.
func (s *Service) Resolve(r *http.Request) (User, error) {
	return s.resolve(r)
}

// Middleware ensures every request carries a guest user. A request with a
// valid session keeps its user. Any other request mints a fresh guest and
// sets its cookie, so the response orients the next visit. A revoked
// cookie never resolves again, so the request it rides on still fails
// every ownership check. A database fault answers 500.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := s.resolve(r)
		if err == nil {
			next.ServeHTTP(w, r.WithContext(withUser(r.Context(), user)))
			return
		}
		if !errors.Is(err, ErrNoSession) {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		user, sessionID, err := s.mint(r.Context())
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		setSessionCookie(w, sessionID, s)
		next.ServeHTTP(w, r.WithContext(withUser(r.Context(), user)))
	})
}

// Owns reports whether the request user owns ownerID. It reads only the
// context the Middleware set, so it never touches the database. An empty
// ownerID never matches.
func (s *Service) Owns(ctx context.Context, ownerID string) bool {
	user, ok := UserFromContext(ctx)
	if !ok || ownerID == "" {
		return false
	}
	return user.ID == ownerID
}

// Revoke marks a session revoked. The next request carrying it resolves
// as anonymous, because every request reads the rows. Revoking an unknown
// id succeeds, since an unknown id is already unusable.
func (s *Service) Revoke(ctx context.Context, sessionID string) error {
	if _, err := s.db.Writer().ExecContext(ctx, "UPDATE guest_sessions SET revoked = 1 WHERE id = ?", sessionID); err != nil {
		return fmt.Errorf("identity: revoke session: %w", err)
	}
	return nil
}

// AuthorizeMedia reports whether a request may read a private blob. It
// fits mediastore.Config.Authorize directly. A public blob passes. Any
// other blob passes only when the request session owns the owner name
// the blob was persisted with. Persist each private blob with Owner set
// to its user id. A refusal returns false, and the media store answers
// the same 404 as an unknown id.
func (s *Service) AuthorizeMedia(r *http.Request, blob mediastore.Blob) bool {
	if blob.Visibility == mediastore.Public {
		return true
	}
	user, err := s.resolve(r)
	if err != nil {
		return false
	}
	return blob.Owner != "" && user.ID == blob.Owner
}

// UserFromContext returns the user the Middleware resolved for this
// request. It returns false when no middleware ran.
func UserFromContext(ctx context.Context) (User, bool) {
	user, ok := ctx.Value(contextKey{}).(User)
	return user, ok
}

// resolve validates the request cookie against the rows. A valid session
// touches the last seen time before it returns.
func (s *Service) resolve(r *http.Request) (User, error) {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return User{}, ErrNoSession
	}
	sessionID, ok := s.verify(cookie.Value)
	if !ok {
		return User{}, ErrNoSession
	}
	var user User
	var revoked int
	var created, seen int64
	err = s.db.Reader().QueryRowContext(r.Context(), `SELECT u.id, u.kind, u.created_at, u.last_seen_at, s.revoked
		FROM guest_sessions s JOIN users u ON u.id = s.user_id WHERE s.id = ?`, sessionID).Scan(
		&user.ID, &user.Kind, &created, &seen, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNoSession
	}
	if err != nil {
		return User{}, fmt.Errorf("identity: resolve session: %w", err)
	}
	if revoked != 0 {
		return User{}, ErrNoSession
	}
	user.CreatedAt = time.Unix(created, 0)
	stamp := s.now().Unix()
	if _, err := s.db.Writer().ExecContext(r.Context(), "UPDATE users SET last_seen_at = ? WHERE id = ?", stamp, user.ID); err != nil {
		return User{}, fmt.Errorf("identity: touch last seen: %w", err)
	}
	user.LastSeen = time.Unix(stamp, 0)
	return user, nil
}

// mint creates a guest user row and its session row in one transaction.
// Either both rows land or neither does.
func (s *Service) mint(ctx context.Context) (User, string, error) {
	userID, err := id.New()
	if err != nil {
		return User{}, "", fmt.Errorf("identity: mint guest: %w", err)
	}
	sessionID, err := id.New()
	if err != nil {
		return User{}, "", fmt.Errorf("identity: mint guest: %w", err)
	}
	stamp := s.now().Unix()
	tx, err := s.db.Writer().BeginTx(ctx, nil)
	if err != nil {
		return User{}, "", fmt.Errorf("identity: mint guest: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, "INSERT INTO users (id, kind, created_at, last_seen_at) VALUES (?, ?, ?, ?)", userID, KindGuest, stamp, stamp); err != nil {
		return User{}, "", fmt.Errorf("identity: mint guest: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO guest_sessions (id, user_id, created_at, revoked) VALUES (?, ?, ?, 0)", sessionID, userID, stamp); err != nil {
		return User{}, "", fmt.Errorf("identity: mint guest: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, "", fmt.Errorf("identity: mint guest: %w", err)
	}
	committed = true
	at := time.Unix(stamp, 0)
	return User{ID: userID, Kind: KindGuest, CreatedAt: at, LastSeen: at}, sessionID, nil
}

// sign authenticates a session id for its cookie value.
func (s *Service) sign(sessionID string) string {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(sessionID))
	return sessionID + "." + hex.EncodeToString(mac.Sum(nil))
}

// verify splits a cookie value and checks its signature in constant time.
// It returns false for any malformed or forged value.
func (s *Service) verify(value string) (string, bool) {
	sessionID, sig, found := strings.Cut(value, ".")
	if !found || sessionID == "" || sig == "" {
		return "", false
	}
	if !id.Valid(sessionID) {
		return "", false
	}
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(sessionID))
	want, err := hex.DecodeString(sig)
	if err != nil {
		return "", false
	}
	if !hmac.Equal(mac.Sum(nil), want) {
		return "", false
	}
	return sessionID, true
}

// setSessionCookie writes the session cookie for a minted session. The
// value carries only the session id and its signature, never user data.
func setSessionCookie(w http.ResponseWriter, sessionID string, s *Service) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    s.sign(sessionID),
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionLifetime.Seconds()),
		Expires:  s.now().Add(sessionLifetime),
	})
}

// contextKey carries the resolved user. Its unexported type keeps other
// packages from colliding with it.
type contextKey struct{}

// withUser stores the resolved user in the request context.
func withUser(ctx context.Context, user User) context.Context {
	return context.WithValue(ctx, contextKey{}, user)
}
