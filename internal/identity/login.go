package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/nrynss/keel/id"
	"github.com/nrynss/keel/wire"
	"github.com/nrynss/reprise/internal/mail"
)

// Envelope codes the sign-in routes answer with. Screens branch on these
// and never on wording.
const (
	// CodeInvalidCode answers a code the server refuses. The code is
	// unknown, requested by another session, expired, used, or past its
	// attempts. One code covers every case, so the answer never names an
	// account.
	CodeInvalidCode = "invalid_code"
	// CodeDiaryConflict answers a sign-in that would strand the guest
	// diary on this device. The device keeps its diary until the caller
	// repeats the verify with the switch choice.
	CodeDiaryConflict = "diary_conflict"
	// CodeInvalidRequest answers a malformed body or a value the route
	// cannot honour.
	CodeInvalidRequest = "invalid_request"
	// CodeInternal answers a dependency fault.
	CodeInternal = "internal_error"
)

// Sign-in code bounds. The code is six digits from the operating system
// generator and lasts ten minutes. Five wrong tries still leave a correct
// code usable. The sixth wrong try closes the code.
const (
	loginCodeExpiry   = 10 * time.Minute
	loginCodeWrongCap = 6
	loginCodeModulus  = 1000000
	loginCodeMaxBody  = 1 << 20
	loginChoiceSwitch = "switch"
	emailProvider     = "email"
)

// ErrInvalidCode reports a sign-in code the server refuses. The answer
// never says which guard tripped, so it never names an account.
var ErrInvalidCode = errors.New("identity: invalid code")

// ErrDiaryConflict reports a sign-in that would strand a guest diary on
// this device. Nothing changes, and the caller repeats the verify with
// the switch choice to move anyway.
var ErrDiaryConflict = errors.New("identity: diary conflict")

// ErrLoginNotConfigured reports a code route the process cannot serve.
// The code key or the mail sender is missing, so the route answers 500
// until the boot wires both.
var ErrLoginNotConfigured = errors.New("identity: login mail is not configured")

// Sender sends one sign-in mail. The mail package implements it, and the
// service declares it here, so tests bind the fake without a provider.
type Sender interface {
	// Send delivers one message and returns the provider message id.
	Send(ctx context.Context, msg mail.Message) (mail.SendResult, error)
}

var (
	_ Sender = (*mail.Client)(nil)
	_ Sender = (*mail.Fake)(nil)
)

// LoginOutcome carries a successful sign-in. UserID owns the device now.
// SessionID is the fresh session the handler sets as a cookie, and the
// old session row is revoked. Switched reports the device moved to
// another user, leaving its guest diary to retention.
type LoginOutcome struct {
	// UserID owns the device after the sign-in.
	UserID string
	// SessionID is the fresh session id for the response cookie.
	SessionID string
	// Switched reports a move to another user.
	Switched bool
}

// normalizeAddress trims and lowercases one address. The hash, the send,
// and the identity subject all read this form, so one typed variant maps
// to one stored row.
func normalizeAddress(address string) string {
	return strings.ToLower(strings.TrimSpace(address))
}

// hashValue authenticates one address or code value with the login key.
// A stolen database copy verifies nothing and reveals neither.
func (s *Service) hashValue(value string) string {
	mac := hmac.New(sha256.New, s.codeKey)
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

// mintLoginCode draws six digits from the operating system generator.
func mintLoginCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(loginCodeModulus))
	if err != nil {
		return "", fmt.Errorf("identity: mint code: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// RequestLoginCode stores a fresh sign-in code for one session and mails
// it. It answers nil for every well formed address, whether or not an
// account holds it, so the caller never learns who registered. A new
// request retires earlier unused codes for the same address and session.
// A mail failure removes the stored code and reports the failure, so no
// dead code lingers. The stored row never holds the plain address. The
// send reads the normalized address from the request, and verify
// re-derives its hash the same way, so storage keeps hashes only.
func (s *Service) RequestLoginCode(ctx context.Context, sessionID, email string) error {
	address := normalizeAddress(email)
	if address == "" {
		return fmt.Errorf("%w: address must not be empty", ErrInvalid)
	}
	if len(s.codeKey) == 0 || s.mail == nil {
		return ErrLoginNotConfigured
	}
	code, err := mintLoginCode()
	if err != nil {
		return err
	}
	codeID, err := id.New()
	if err != nil {
		return fmt.Errorf("identity: mint code: %w", err)
	}
	now := s.now().Unix()
	tx, err := s.db.Writer().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("identity: store code: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `UPDATE login_codes SET used_at = ?, address = ''
		WHERE address_hash = ? AND requesting_session = ? AND used_at = 0`,
		now, s.hashValue(address), sessionID); err != nil {
		return fmt.Errorf("identity: store code: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO login_codes
		(id, address_hash, address, code_hash, requesting_session, expires_at, attempts, used_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 0, 0, ?)`,
		codeID, s.hashValue(address), "", s.hashValue(code),
		sessionID, now+int64(loginCodeExpiry.Seconds()), now); err != nil {
		return fmt.Errorf("identity: store code: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("identity: store code: %w", err)
	}
	committed = true
	text := "Your Reprise sign-in code is " + code + ". It expires in 10 minutes. If you did not ask for it, ignore this mail."
	if _, err := s.mail.Send(ctx, mail.Message{
		To:      address,
		Subject: "Your Reprise sign-in code",
		Text:    text,
	}); err != nil {
		_, _ = s.db.Writer().ExecContext(ctx, "DELETE FROM login_codes WHERE id = ?", codeID)
		return fmt.Errorf("identity: send code: %w", err)
	}
	return nil
}

// loginCodeRow carries the one stored code a verify may consume.
type loginCodeRow struct {
	id       string
	hash     string
	attempts int64
	expires  int64
}

// closeCode retires one code row and clears its address field as belt.
// Storage keeps hashes only, so the clear guards rows older than that rule.
func (s *Service) closeCode(ctx context.Context, codeID string) error {
	if _, err := s.db.Writer().ExecContext(ctx,
		"UPDATE login_codes SET used_at = ?, address = '' WHERE id = ?",
		s.now().Unix(), codeID); err != nil {
		return fmt.Errorf("identity: close code: %w", err)
	}
	return nil
}

// checkCode finds the live code for one address and session and checks
// the typed value against it. A mismatch counts one attempt, and the
// sixth wrong try closes the code. An expired or exhausted code closes
// on sight. It returns the row id on a match and ErrInvalidCode on any
// refusal, so the answer never says which guard tripped.
func (s *Service) checkCode(ctx context.Context, address, sessionID, code string) (string, error) {
	var row loginCodeRow
	err := s.db.Reader().QueryRowContext(ctx, `SELECT id, code_hash, attempts, expires_at FROM login_codes
		WHERE address_hash = ? AND requesting_session = ? AND used_at = 0
		ORDER BY created_at DESC, id DESC LIMIT 1`,
		s.hashValue(address), sessionID).Scan(&row.id, &row.hash, &row.attempts, &row.expires)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrInvalidCode
	}
	if err != nil {
		return "", fmt.Errorf("identity: read code: %w", err)
	}
	if row.expires <= s.now().Unix() || row.attempts >= loginCodeWrongCap {
		if err := s.closeCode(ctx, row.id); err != nil {
			return "", err
		}
		return "", ErrInvalidCode
	}
	want, err := hex.DecodeString(row.hash)
	if err != nil {
		return "", fmt.Errorf("identity: decode code hash: %w", err)
	}
	mac := hmac.New(sha256.New, s.codeKey)
	_, _ = mac.Write([]byte(strings.TrimSpace(code)))
	if !hmac.Equal(mac.Sum(nil), want) {
		if row.attempts+1 >= loginCodeWrongCap {
			if err := s.closeCode(ctx, row.id); err != nil {
				return "", err
			}
		} else if _, err := s.db.Writer().ExecContext(ctx,
			"UPDATE login_codes SET attempts = ? WHERE id = ?", row.attempts+1, row.id); err != nil {
			return "", fmt.Errorf("identity: count attempt: %w", err)
		}
		return "", ErrInvalidCode
	}
	return row.id, nil
}

// VerifyLoginCode checks one code and resolves the login. It accepts only
// a code requested by this same session, unexpired, unused, and within
// five wrong attempts. On success it consumes the code, attaches or
// resumes the identity, and rotates the session. A conflict without the
// switch choice changes nothing and reports ErrDiaryConflict, so the
// caller repeats the verify to move anyway.
func (s *Service) VerifyLoginCode(ctx context.Context, sessionID, userID, email, code, choice string) (LoginOutcome, error) {
	address := normalizeAddress(email)
	if address == "" || strings.TrimSpace(code) == "" {
		return LoginOutcome{}, fmt.Errorf("%w: address and code are required", ErrInvalid)
	}
	if choice != "" && choice != loginChoiceSwitch {
		return LoginOutcome{}, fmt.Errorf("%w: unknown choice", ErrInvalid)
	}
	if len(s.codeKey) == 0 {
		return LoginOutcome{}, ErrLoginNotConfigured
	}
	codeID, err := s.checkCode(ctx, address, sessionID, code)
	if err != nil {
		return LoginOutcome{}, err
	}
	var holder string
	err = s.db.Reader().QueryRowContext(ctx,
		"SELECT user_id FROM identities WHERE provider = ? AND subject = ?",
		emailProvider, address).Scan(&holder)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return LoginOutcome{}, fmt.Errorf("identity: read identity: %w", err)
	}
	if errors.Is(err, sql.ErrNoRows) {
		holder = ""
	}
	var episodes int
	if holder != "" && holder != userID {
		if err := s.db.Reader().QueryRowContext(ctx,
			"SELECT COUNT(*) FROM episodes WHERE owner_id = ?", userID).Scan(&episodes); err != nil {
			return LoginOutcome{}, fmt.Errorf("identity: count guest diary: %w", err)
		}
		if episodes > 0 && choice != loginChoiceSwitch {
			return LoginOutcome{}, ErrDiaryConflict
		}
	}
	now := s.now().Unix()
	tx, err := s.db.Writer().BeginTx(ctx, nil)
	if err != nil {
		return LoginOutcome{}, fmt.Errorf("identity: resolve login: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	consumed, err := tx.ExecContext(ctx,
		"UPDATE login_codes SET used_at = ?, address = '' WHERE id = ? AND used_at = 0",
		now, codeID)
	if err != nil {
		return LoginOutcome{}, fmt.Errorf("identity: resolve login: %w", err)
	}
	if n, err := consumed.RowsAffected(); err != nil || n != 1 {
		if err != nil {
			return LoginOutcome{}, fmt.Errorf("identity: resolve login: %w", err)
		}
		return LoginOutcome{}, ErrInvalidCode
	}
	target := userID
	switched := false
	switch {
	case holder == "":
		identityID, err := id.New()
		if err != nil {
			return LoginOutcome{}, fmt.Errorf("identity: resolve login: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO identities
			(id, user_id, provider, subject, created_at) VALUES (?, ?, ?, ?, ?)`,
			identityID, userID, emailProvider, address, now); err != nil {
			// Another device attached this address first, so the
			// insert above collided on the provider subject pair.
			// Join that holder instead of failing, and honour the
			// diary conflict exactly as a later verify would.
			var fresh string
			if rerr := tx.QueryRowContext(ctx,
				"SELECT user_id FROM identities WHERE provider = ? AND subject = ?",
				emailProvider, address).Scan(&fresh); rerr != nil {
				return LoginOutcome{}, fmt.Errorf("identity: resolve login: %w", err)
			}
			if fresh != userID {
				if cerr := tx.QueryRowContext(ctx,
					"SELECT COUNT(*) FROM episodes WHERE owner_id = ?", userID).Scan(&episodes); cerr != nil {
					return LoginOutcome{}, fmt.Errorf("identity: resolve login: %w", cerr)
				}
				if episodes > 0 && choice != loginChoiceSwitch {
					return LoginOutcome{}, ErrDiaryConflict
				}
				target = fresh
				switched = true
			}
		} else if _, err := tx.ExecContext(ctx,
			"UPDATE users SET kind = ? WHERE id = ?", KindOwner, userID); err != nil {
			return LoginOutcome{}, fmt.Errorf("identity: resolve login: %w", err)
		}
	case holder != userID:
		target = holder
		switched = true
	}
	nextSession, err := id.New()
	if err != nil {
		return LoginOutcome{}, fmt.Errorf("identity: resolve login: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO guest_sessions
		(id, user_id, created_at, revoked) VALUES (?, ?, ?, 0)`,
		nextSession, target, now); err != nil {
		return LoginOutcome{}, fmt.Errorf("identity: resolve login: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE guest_sessions SET revoked = 1 WHERE id = ?", sessionID); err != nil {
		return LoginOutcome{}, fmt.Errorf("identity: resolve login: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LoginOutcome{}, fmt.Errorf("identity: resolve login: %w", err)
	}
	committed = true
	return LoginOutcome{UserID: target, SessionID: nextSession, Switched: switched}, nil
}

// LoginHandler serves the sign-in code routes on one handler. The route
// table mounts it behind the guest middleware, so every request carries
// a user and a session. A nil mail sender answers 500, so wiring faults
// surface instead of hiding.
func (s *Service) LoginHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login/code", s.handleLoginCode)
	mux.HandleFunc("POST /api/login/verify", s.handleLoginVerify)
	return mux
}

// loginCodeRequestJSON carries the address a code goes to.
type loginCodeRequestJSON struct {
	// Email is the address the code goes to, before normalization.
	Email string `json:"email"`
}

// loginVerifyRequestJSON carries one typed code with its address. Choice
// carries "switch" when the caller moves to the account despite a guest
// diary on this device.
type loginVerifyRequestJSON struct {
	// Email is the address the code went to, before normalization.
	Email string `json:"email"`
	// Code is the typed six digit value.
	Code string `json:"code"`
	// Choice carries "switch" on a repeat verify past a conflict.
	Choice string `json:"choice"`
}

// loginOKJSON answers a code request and a successful verify. The code
// answer is identical for known and unknown addresses.
type loginOKJSON struct {
	// OK reports the request landed.
	OK bool `json:"ok"`
}

// decodeLoginBody reads one JSON body up to the cap. It reports false
// when the body is missing or malformed, and the caller answers invalid
// request.
func decodeLoginBody(w http.ResponseWriter, r *http.Request, shape any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, loginCodeMaxBody)
	if err := json.NewDecoder(r.Body).Decode(shape); err != nil {
		_ = wire.WriteError(w, http.StatusBadRequest, CodeInvalidRequest, "this request carries no usable body", nil)
		return false
	}
	return true
}

// writeLoginJSON answers with one named payload. Every login answer
// travels no-store, so a shared cache never keeps it.
func writeLoginJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// loginSession returns the user and session the middleware resolved for
// this request. It answers 500 when no middleware ran, which never
// happens behind the route table mount.
func loginSession(w http.ResponseWriter, r *http.Request) (User, string, bool) {
	user, ok := UserFromContext(r.Context())
	sessionID, sok := SessionIDFromContext(r.Context())
	if !ok || !sok || user.ID == "" || sessionID == "" {
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the guest session is not wired", nil)
		return User{}, "", false
	}
	return user, sessionID, true
}

// handleLoginCode answers the code route. It stores a fresh code for the
// requesting session, mails it, and answers the same 202 body whether or
// not the address holds an account.
func (s *Service) handleLoginCode(w http.ResponseWriter, r *http.Request) {
	_, sessionID, ok := loginSession(w, r)
	if !ok {
		return
	}
	var body loginCodeRequestJSON
	if !decodeLoginBody(w, r, &body) {
		return
	}
	if err := s.RequestLoginCode(r.Context(), sessionID, body.Email); err != nil {
		if errors.Is(err, ErrInvalid) {
			_ = wire.WriteError(w, http.StatusBadRequest, CodeInvalidRequest, "this request names no address", nil)
			return
		}
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the sign-in code could not be sent", nil)
		return
	}
	writeLoginJSON(w, http.StatusAccepted, loginOKJSON{OK: true})
}

// handleLoginVerify answers the verify route. A refused code answers 401
// without saying which guard tripped. A conflict answers 409 and changes
// nothing. Any success rotates the session and answers 200.
func (s *Service) handleLoginVerify(w http.ResponseWriter, r *http.Request) {
	user, sessionID, ok := loginSession(w, r)
	if !ok {
		return
	}
	var body loginVerifyRequestJSON
	if !decodeLoginBody(w, r, &body) {
		return
	}
	outcome, err := s.VerifyLoginCode(r.Context(), sessionID, user.ID, body.Email, body.Code, body.Choice)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalid):
			_ = wire.WriteError(w, http.StatusBadRequest, CodeInvalidRequest, "this request carries no usable code", nil)
		case errors.Is(err, ErrInvalidCode):
			_ = wire.WriteError(w, http.StatusUnauthorized, CodeInvalidCode, "this code is expired, used, or never requested on this device", nil)
		case errors.Is(err, ErrDiaryConflict):
			_ = wire.WriteError(w, http.StatusConflict, CodeDiaryConflict, "this device holds a guest diary the account would leave behind", nil)
		default:
			_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the sign-in could not complete", nil)
		}
		return
	}
	s.SetSessionCookie(w, outcome.SessionID)
	writeLoginJSON(w, http.StatusOK, loginOKJSON{OK: true})
}
