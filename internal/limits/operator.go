package limits

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/nrynss/keel/wire"
	"github.com/nrynss/reprise/internal/identity"
)

// OperatorIdentity is one sign-in key a user holds. Provider names the
// sign-in method and Subject names the account behind it.
type OperatorIdentity struct {
	// Provider names the sign-in method, such as email or google.
	Provider string
	// Subject names the account, such as the address or the issuer sub.
	Subject string
}

// IdentitySource lists the sign-in keys one user holds. The sign-in
// service implements it. Tests stub it.
type IdentitySource interface {
	// IdentitiesForUser returns every sign-in key attached to userID.
	IdentitiesForUser(ctx context.Context, userID string) ([]OperatorIdentity, error)
}

// RequestUsers resolves the caller behind the guest session middleware.
// It returns the user id and kind, and false when no session resolved.
type RequestUsers func(r *http.Request) (userID, kind string, ok bool)

// RequestUser resolves the caller through the guest middleware context.
// It returns false when no middleware ran or no session resolved.
func RequestUser(r *http.Request) (string, string, bool) {
	user, ok := identity.UserFromContext(r.Context())
	if !ok || user.ID == "" {
		return "", "", false
	}
	return user.ID, user.Kind, true
}

// OperatorAuth gates admin endpoints behind the operators setting. It
// implements OwnerAuth, so wiring swaps it in without touching the
// service or the handler. The check reads only, so it changes no
// ownership and no seed state.
type OperatorAuth struct {
	operators  []string
	users      RequestUsers
	identities IdentitySource
}

// NewOperatorAuth returns the operator check over operators entries.
// Each entry reads email:<address> or google:<sub>. A nil users
// resolver defaults to RequestUser, which reads the guest middleware
// context. identities must not be nil, because a missing source would
// refuse every caller alike.
func NewOperatorAuth(operators []string, users RequestUsers, identities IdentitySource) (*OperatorAuth, error) {
	if identities == nil {
		return nil, fmt.Errorf("limits: new operator auth: %w: identities must not be nil", ErrInvalid)
	}
	if users == nil {
		users = RequestUser
	}
	return &OperatorAuth{operators: operators, users: users, identities: identities}, nil
}

// Authorize wraps next and allows only listed operators through. A
// request with no signed-in caller gets 401. A signed-in caller with
// no listed identity gets 403. Both refusals carry the operator code,
// so screens branch on one value and read the status for the sign-in
// state.
func (a *OperatorAuth) Authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a == nil || a.identities == nil {
			_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the operator check is not wired", nil)
			return
		}
		users := a.users
		if users == nil {
			users = RequestUser
		}
		userID, kind, ok := users(r)
		if !ok || userID == "" || kind != identity.KindOwner {
			_ = wire.WriteError(w, http.StatusUnauthorized, CodeOperatorRequired, "the admin page needs a signed-in operator", nil)
			return
		}
		held, err := a.identities.IdentitiesForUser(r.Context(), userID)
		if err != nil {
			_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the operator check could not be read", nil)
			return
		}
		if isOperator(a.operators, held) {
			next.ServeHTTP(w, r)
			return
		}
		_ = wire.WriteError(w, http.StatusForbidden, CodeOperatorRequired, "the admin page needs an operator identity", nil)
	})
}

// isOperator reports whether any listed entry matches a held identity.
// Only email and google entries count, so a mistyped prefix never
// matches.
func isOperator(operators []string, held []OperatorIdentity) bool {
	keys := make(map[string]bool, len(held))
	for _, id := range held {
		key, ok := operatorKey(id.Provider, id.Subject)
		if !ok {
			continue
		}
		keys[key] = true
	}
	for _, entry := range operators {
		provider, subject, ok := splitOperatorEntry(entry)
		if !ok {
			continue
		}
		key, ok := operatorKey(provider, subject)
		if !ok {
			continue
		}
		if keys[key] {
			return true
		}
	}
	return false
}

// splitOperatorEntry splits one operators entry at its first colon. It
// reports false when either side is empty.
func splitOperatorEntry(entry string) (string, string, bool) {
	provider, subject, found := strings.Cut(entry, ":")
	if !found {
		return "", "", false
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	subject = strings.TrimSpace(subject)
	if provider == "" || subject == "" {
		return "", "", false
	}
	return provider, subject, true
}

// operatorKey builds the comparison key for one provider subject pair.
// Email addresses compare case-insensitively after trimming, the way
// the code flow normalizes them. Issuer subs compare exactly. It
// reports false for any other provider and for an empty subject.
func operatorKey(provider, subject string) (string, bool) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	switch provider {
	case "email":
		subject = strings.ToLower(strings.TrimSpace(subject))
	case "google":
		subject = strings.TrimSpace(subject)
	default:
		return "", false
	}
	if subject == "" {
		return "", false
	}
	return provider + ":" + subject, true
}
