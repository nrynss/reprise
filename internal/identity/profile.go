package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"

	"github.com/nrynss/keel/wire"
)

// Route patterns for the account profile. The boot mounts them behind the
// guest middleware beside the sign-in routes, so every request carries a
// user and a session.
const (
	// PatternAccount reads the caller account with its display name.
	PatternAccount = "/api/account"
	// PatternAccountName writes the caller display name.
	PatternAccountName = "/api/account/name"
)

// Profile envelope codes the account screens branch on. Codes stay stable
// and wording stays free.
const (
	// CodeProfileUnauthorized answers a guest with no sign-in. Guests hold
	// no address, so there is no account to name.
	CodeProfileUnauthorized = "unauthorized"
	// CodeProfileInvalid answers a name the server refuses: too long or
	// carrying control characters.
	CodeProfileInvalid = "invalid_request"
)

// ErrInvalidName reports a display name the server refuses. The name is
// too long or carries control characters.
var ErrInvalidName = errors.New("identity: invalid display name")

// MaxDisplayName is the longest display name in runes. It fits a byline
// without wrapping the share page.
const MaxDisplayName = 60

// accountJSON answers the account read and the name write. Email is the
// caller own address. DisplayName is the trimmed name or empty while the
// caller never set one.
type accountJSON struct {
	// Email is the caller own address.
	Email string `json:"email"`
	// DisplayName is the caller display name, empty while unset.
	DisplayName string `json:"display_name"`
}

// nameRequestJSON carries one display name write. DisplayName is the form
// the account page sends. Name is the short alias the same page used
// first, and the server reads either key so one field renames nothing.
type nameRequestJSON struct {
	// DisplayName is the name under its column key.
	DisplayName string `json:"display_name"`
	// Name is the same value under the short key.
	Name string `json:"name"`
}

// ProfileHandler serves the account read and the display name write on one
// handler. The boot mounts it behind the guest middleware, so every request
// carries a user and a session. Both routes need a signed-in user, and a
// guest answers 401 without revealing anything else.
func (s *Service) ProfileHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(PatternAccount, s.handleAccount)
	mux.HandleFunc(PatternAccountName, s.handleAccountName)
	return mux
}

// handleAccount answers GET with the caller address beside its display
// name. A guest answers 401, because there is no account to read.
func (s *Service) handleAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		_ = wire.WriteError(w, http.StatusMethodNotAllowed, CodeInvalidRequest, "the account reads with GET", nil)
		return
	}
	user, _, ok := loginSession(w, r)
	if !ok {
		return
	}
	email, name, err := s.accountOf(r.Context(), user.ID)
	if err != nil {
		if errors.Is(err, ErrNoSession) {
			_ = wire.WriteError(w, http.StatusUnauthorized, CodeProfileUnauthorized, "this account needs a sign-in", nil)
			return
		}
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the account could not be read", nil)
		return
	}
	writeLoginJSON(w, http.StatusOK, accountJSON{Email: email, DisplayName: name})
}

// handleAccountName answers PUT by storing the caller display name. The
// name is trimmed, at most 60 characters, with no control characters. An
// empty name clears the stored value. A guest answers 401 and stores
// nothing.
func (s *Service) handleAccountName(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		_ = wire.WriteError(w, http.StatusMethodNotAllowed, CodeInvalidRequest, "the name writes with PUT", nil)
		return
	}
	user, _, ok := loginSession(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, loginCodeMaxBody)
	var body nameRequestJSON
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		_ = wire.WriteError(w, http.StatusBadRequest, CodeProfileInvalid, "this request carries no usable name", nil)
		return
	}
	raw := body.DisplayName
	if raw == "" {
		raw = body.Name
	}
	clean, err := normalizeDisplayName(raw)
	if err != nil {
		_ = wire.WriteError(w, http.StatusBadRequest, CodeProfileInvalid, "this name is too long or carries control characters", nil)
		return
	}
	email, err := s.SetDisplayName(r.Context(), user.ID, clean)
	if err != nil {
		if errors.Is(err, ErrNoSession) {
			_ = wire.WriteError(w, http.StatusUnauthorized, CodeProfileUnauthorized, "this account needs a sign-in", nil)
			return
		}
		if errors.Is(err, ErrInvalidName) {
			_ = wire.WriteError(w, http.StatusBadRequest, CodeProfileInvalid, "this name is too long or carries control characters", nil)
			return
		}
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the name could not be saved", nil)
		return
	}
	writeLoginJSON(w, http.StatusOK, accountJSON{Email: email, DisplayName: clean})
}

// accountOf returns the caller address with its display name. A user with
// no email identity is a guest, and reports ErrNoSession so the caller
// answers 401. A missing display column reads empty, so a database that
// migrated before the name column still answers.
func (s *Service) accountOf(ctx context.Context, userID string) (string, string, error) {
	email, err := s.signedInEmail(ctx, userID)
	if err != nil {
		return "", "", err
	}
	name, err := s.displayNameOf(ctx, userID)
	if err != nil {
		return "", "", err
	}
	return email, name, nil
}

// signedInEmail returns the caller email address, or ErrNoSession while the
// user holds no email identity. Google-only sign-ins hold no email row, so
// they read as guests here until they attach one.
func (s *Service) signedInEmail(ctx context.Context, userID string) (string, error) {
	keys, err := s.IdentitiesForUser(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("identity: read account: %w", err)
	}
	for _, key := range keys {
		if key.Provider == emailProvider {
			return key.Subject, nil
		}
	}
	return "", ErrNoSession
}

// displayNameOf returns the stored display name or empty. A missing column
// reads empty instead of failing, so older copies keep serving the account
// read while the migration catches up.
func (s *Service) displayNameOf(ctx context.Context, userID string) (string, error) {
	var name sql.NullString
	if err := s.db.Reader().QueryRowContext(ctx,
		"SELECT display_name FROM users WHERE id = ?", userID).Scan(&name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("identity: read account: %w", ErrNoSession)
		}
		if missingDisplayColumn(err) {
			return "", nil
		}
		return "", fmt.Errorf("identity: read account: %w", err)
	}
	if !name.Valid {
		return "", nil
	}
	return name.String, nil
}

// DisplayName returns the stored display name for userID, or empty while
// unset. Callers that need the address read it separately, so this never
// exposes one.
func (s *Service) DisplayName(ctx context.Context, userID string) (string, error) {
	if userID == "" {
		return "", fmt.Errorf("%w: user id must not be empty", ErrInvalid)
	}
	return s.displayNameOf(ctx, userID)
}

// SetDisplayName stores the already trimmed display name for a signed-in
// user and returns its address. A guest reports ErrNoSession and stores
// nothing. An overlong name or one with control characters reports
// ErrInvalidName. An empty name clears the stored value. A missing column
// migrates first, so the write never fails on an older copy.
func (s *Service) SetDisplayName(ctx context.Context, userID, name string) (string, error) {
	if userID == "" {
		return "", fmt.Errorf("%w: user id must not be empty", ErrInvalid)
	}
	clean, err := normalizeDisplayName(name)
	if err != nil {
		return "", err
	}
	email, err := s.signedInEmail(ctx, userID)
	if err != nil {
		return "", err
	}
	if err := s.ensureDisplayColumn(ctx); err != nil {
		return "", err
	}
	if _, err := s.db.Writer().ExecContext(ctx,
		"UPDATE users SET display_name = ? WHERE id = ?", clean, userID); err != nil {
		return "", fmt.Errorf("identity: store display name: %w", err)
	}
	return email, nil
}

// normalizeDisplayName trims one display name and checks its length and
// characters. It returns the trimmed value. An empty input clears, so it
// returns empty with no error.
func normalizeDisplayName(raw string) (string, error) {
	clean := strings.TrimSpace(raw)
	if len([]rune(clean)) > MaxDisplayName {
		return "", fmt.Errorf("%w: name must be at most %d characters", ErrInvalidName, MaxDisplayName)
	}
	for _, r := range clean {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%w: name must not carry control characters", ErrInvalidName)
		}
	}
	return clean, nil
}

// ensureDisplayColumn adds the display column when an older copy lacks it.
// Fresh copies already carry it through migrations, so this only runs for
// databases that opened before the column landed.
func (s *Service) ensureDisplayColumn(ctx context.Context) error {
	var name string
	err := s.db.Reader().QueryRowContext(ctx,
		"SELECT name FROM pragma_table_info('users') WHERE name = 'display_name'").Scan(&name)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("identity: check display column: %w", err)
	}
	if name == "display_name" {
		return nil
	}
	if _, err := s.db.Writer().ExecContext(ctx,
		"ALTER TABLE users ADD COLUMN display_name TEXT NOT NULL DEFAULT ''"); err != nil {
		if missingDisplayColumn(err) {
			return nil
		}
		return fmt.Errorf("identity: add display column: %w", err)
	}
	return nil
}

// missingDisplayColumn reports whether err names the absent display column.
// The share read and the ensure step both treat it as empty instead of a
// fault, so older copies keep serving while the migration catches up.
func missingDisplayColumn(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "display_name")
}
