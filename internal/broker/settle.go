// Settle linkage lives here. The broker mints the lease and the budget
// hold behind the token, but no diary column links either row to the
// session row yet. This file keeps that linkage in a broker owned table,
// so the session end path and the abandoned sweep can both rebuild the
// reconciler input after the minting request is gone. It also adapts the
// provider sessions client to the reader and ender seams, so the binary
// passes one value where reconciliation and the sweep each need one.
package broker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	costsqlitestore "github.com/nrynss/keel/cost/sqlitestore"
	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/assemblyai"
)

// ErrUnknownSession reports a settle for a session id the linkage table
// holds nothing for. The mint records the link, so an unknown id means
// the session predates the linkage or never minted here.
var ErrUnknownSession = errors.New("broker: unknown session")

// ErrNoClose reports a settle for a session whose provider close never
// landed. The browser records the close on its end call, and the sweep
// only lists rows that carry one, so this caller waits instead of
// pricing silence.
var ErrNoClose = errors.New("broker: session has no provider close")

// ensureLinkTable creates the settle linkage table beside every other
// broker owned table. Mint rows land here, and settle and sweep reads
// join them with the diary session rows.
func ensureLinkTable(ctx context.Context, db *sqlite.DB) error {
	const schema = `CREATE TABLE IF NOT EXISTS session_settle (
		session_id TEXT PRIMARY KEY,
		owner_id TEXT NOT NULL,
		episode_id TEXT NOT NULL,
		lease_id TEXT NOT NULL,
		reservation TEXT NOT NULL,
		token_cap INTEGER NOT NULL,
		minted_at INTEGER NOT NULL
	)`
	if _, err := db.Writer().ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("broker: create settle linkage: %w", ErrState)
	}
	return nil
}

// recordLink stores the mint time linkage for one session. It runs after
// the diary write on the success path only, so a refused mint holds
// nothing and links nothing.
func (b *Broker) recordLink(ctx context.Context, ownerID, episodeID, sessionID, leaseID string, reservation costsqlitestore.Reservation) error {
	if b.db == nil {
		return fmt.Errorf("broker: record linkage: %w: database must not be nil", ErrInvalid)
	}
	raw, err := json.Marshal(reservation)
	if err != nil {
		return fmt.Errorf("broker: record linkage: %w", ErrInvalid)
	}
	if _, err := b.db.Writer().ExecContext(ctx,
		`INSERT INTO session_settle (session_id, owner_id, episode_id, lease_id, reservation, token_cap, minted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sessionID, ownerID, episodeID, leaseID, string(raw), b.cap, time.Now().UnixMilli()); err != nil {
		return fmt.Errorf("broker: record linkage: %w", ErrStore)
	}
	return nil
}

// SettleInput rebuilds the reconciler input for one ended session. It
// reads the mint time linkage and the provider close the end call
// recorded, so the caller passes nothing but the diary session id. An
// empty provider close reports ErrNoClose, and the caller waits for the
// end or the sweep instead of settling silence.
func (b *Broker) SettleInput(ctx context.Context, sessionID string) (Input, error) {
	if sessionID == "" {
		return Input{}, fmt.Errorf("broker: settle input: %w: session id must not be empty", ErrInvalid)
	}
	if b.db == nil {
		return Input{}, fmt.Errorf("broker: settle input: %w: database must not be nil", ErrInvalid)
	}
	var in Input
	var raw string
	in.SessionID = sessionID
	err := b.db.Reader().QueryRowContext(ctx,
		`SELECT owner_id, episode_id, lease_id, reservation, token_cap FROM session_settle WHERE session_id = ?`,
		sessionID).Scan(&in.OwnerID, &in.EpisodeID, &in.LeaseID, &raw, &in.TokenCapSeconds)
	if errors.Is(err, sql.ErrNoRows) {
		return Input{}, fmt.Errorf("broker: settle input %s: %w", sessionID, ErrUnknownSession)
	}
	if err != nil {
		return Input{}, fmt.Errorf("broker: settle input %s: %w", sessionID, ErrStore)
	}
	if err := json.Unmarshal([]byte(raw), &in.Reservation); err != nil {
		return Input{}, fmt.Errorf("broker: settle input %s: %w", sessionID, ErrStore)
	}
	err = b.db.Reader().QueryRowContext(ctx,
		`SELECT provider_session_id FROM sessions WHERE id = ?`, sessionID).Scan(&in.ProviderSessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return Input{}, fmt.Errorf("broker: settle input %s: %w", sessionID, ErrUnknownSession)
	}
	if err != nil {
		return Input{}, fmt.Errorf("broker: settle input %s: %w", sessionID, ErrStore)
	}
	if in.ProviderSessionID == "" {
		return Input{}, fmt.Errorf("broker: settle input %s: %w", sessionID, ErrNoClose)
	}
	return in, nil
}

// ListOpen lists the session links the sweep should inspect, oldest
// first. It carries the mint time linkage each candidate needs, with the
// age measured from the mint. Rows without a recorded provider close
// stay out, because the sweep prices provider truth and an id it cannot
// read would fail the whole pass. Those rows age out of their holds by
// reservation expiry instead.
func (b *Broker) ListOpen(ctx context.Context) ([]Candidate, error) {
	if b.db == nil {
		return nil, fmt.Errorf("broker: list open: %w: database must not be nil", ErrInvalid)
	}
	now := time.Now().UnixMilli()
	rows, err := b.db.Reader().QueryContext(ctx,
		`SELECT l.session_id, l.owner_id, l.episode_id, s.provider_session_id,
			l.lease_id, l.reservation, l.token_cap, l.minted_at
		FROM session_settle l JOIN sessions s ON s.id = l.session_id
		WHERE s.provider_session_id <> ''
		ORDER BY l.minted_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("broker: list open: %w: %w", ErrSweep, err)
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var c Candidate
		var raw string
		var minted int64
		if err := rows.Scan(&c.SessionID, &c.OwnerID, &c.EpisodeID, &c.ProviderSessionID,
			&c.LeaseID, &raw, &c.TokenCapSeconds, &minted); err != nil {
			return nil, fmt.Errorf("broker: list open: %w: %w", ErrSweep, err)
		}
		if err := json.Unmarshal([]byte(raw), &c.Reservation); err != nil {
			return nil, fmt.Errorf("broker: list open: %w: %w", ErrSweep, err)
		}
		age := (now - minted) / 1000
		if age < 0 {
			age = 0
		}
		c.OpenSeconds = int(age)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("broker: list open: %w: %w", ErrSweep, err)
	}
	return out, nil
}

// ceilSeconds rounds provider seconds up to whole seconds. The provider
// bills connected time, so a partial second books as a seconds worth of
// spend rather than vanishing from the ledger.
func ceilSeconds(seconds float64) int {
	if seconds <= 0 {
		return 0
	}
	return int(math.Ceil(seconds))
}

// SessionsAdapter reads and ends provider sessions for the reconciler
// and the sweep over one sessions client. Create it with
// NewSessionsAdapter, because the zero value holds no client.
type SessionsAdapter struct {
	client *assemblyai.SessionsClient
}

var (
	_ SessionReader = (*SessionsAdapter)(nil)
	_ StatusReader  = (*SessionsAdapter)(nil)
	_ SessionEnder  = (*SessionsAdapter)(nil)
)

// NewSessionsAdapter returns the reader and ender over client. A nil
// client fails, so wiring faults surface instead of settling nothing.
func NewSessionsAdapter(client *assemblyai.SessionsClient) (*SessionsAdapter, error) {
	if client == nil {
		return nil, fmt.Errorf("broker: new sessions adapter: %w: client must not be nil", ErrInvalid)
	}
	return &SessionsAdapter{client: client}, nil
}

// ReadSession returns the provider session for the reconciler. A record
// the provider no longer keeps reports ErrRead, because a settle prices
// nothing without a duration. An open record with no duration yet
// reports ErrRead for the same reason, and the sweep path measures open
// sessions from their start time instead.
func (a *SessionsAdapter) ReadSession(ctx context.Context, providerSessionID string) (ProviderSession, error) {
	if a == nil || a.client == nil {
		return ProviderSession{}, fmt.Errorf("broker: read session: %w: adapter has no client", ErrInvalid)
	}
	status, err := a.client.FetchSession(ctx, providerSessionID)
	if errors.Is(err, assemblyai.ErrSessionGone) {
		return ProviderSession{}, fmt.Errorf("broker: read session %s: %w: record is gone", providerSessionID, ErrRead)
	}
	if err != nil {
		return ProviderSession{}, fmt.Errorf("broker: read session %s: %w: %w", providerSessionID, ErrRead, err)
	}
	if status.DurationSeconds == nil || *status.DurationSeconds < 0 {
		return ProviderSession{}, fmt.Errorf("broker: read session %s: %w: duration is missing", providerSessionID, ErrRead)
	}
	return ProviderSession{
		ID:              status.ID,
		DurationSeconds: ceilSeconds(*status.DurationSeconds),
		RecordingURL:    artifactURL(status.Artifacts, "audio"),
		TimelineURL:     artifactURL(status.Artifacts, "timeline"),
	}, nil
}

// ReadStatus returns the provider status for the sweep. A record the
// provider no longer keeps reports ErrProviderGone, so the sweep stops
// for review instead of pricing silence. An open record carries no
// duration, so the sweep books the elapsed time since the provider
// opened it.
func (a *SessionsAdapter) ReadStatus(ctx context.Context, providerSessionID string) (ProviderStatus, error) {
	if a == nil || a.client == nil {
		return ProviderStatus{}, fmt.Errorf("broker: read status: %w: adapter has no client", ErrInvalid)
	}
	status, err := a.client.FetchSession(ctx, providerSessionID)
	if errors.Is(err, assemblyai.ErrSessionGone) {
		return ProviderStatus{}, fmt.Errorf("broker: read status %s: %w", providerSessionID, ErrProviderGone)
	}
	if err != nil {
		return ProviderStatus{}, fmt.Errorf("broker: read status %s: %w: %w", providerSessionID, ErrSweep, err)
	}
	out := ProviderStatus{
		ID:           status.ID,
		Status:       status.Status,
		CloseReason:  status.CloseReason,
		RecordingURL: artifactURL(status.Artifacts, "audio"),
		TimelineURL:  artifactURL(status.Artifacts, "timeline"),
	}
	if status.DurationSeconds != nil && *status.DurationSeconds >= 0 {
		out.HasDuration = true
		out.DurationSeconds = ceilSeconds(*status.DurationSeconds)
		return out, nil
	}
	elapsed := int(time.Since(status.CreatedAt).Seconds())
	if elapsed < 0 {
		elapsed = 0
	}
	out.OpenSeconds = elapsed
	return out, nil
}

// EndSession deletes the provider record after the settle. A record
// already gone reports success without a delete, because ending an
// ended session stays safe.
func (a *SessionsAdapter) EndSession(ctx context.Context, providerSessionID string) (EndResult, error) {
	if a == nil || a.client == nil {
		return EndResult{}, fmt.Errorf("broker: end session: %w: adapter has no client", ErrInvalid)
	}
	res, err := a.client.TerminateSession(ctx, providerSessionID)
	if err != nil {
		return EndResult{}, fmt.Errorf("broker: end session %s: %w: %w", providerSessionID, ErrSweep, err)
	}
	if !res.Deleted {
		return EndResult{Deleted: false, Detail: "provider record already gone"}, nil
	}
	return EndResult{Deleted: true, Detail: "provider record deleted"}, nil
}

// artifactURL picks the download URL of the first artifact of one type.
// Artifact URLs carry their own grant and expire, so the reconciler
// persists the bytes on receipt.
func artifactURL(artifacts []assemblyai.SessionArtifact, kind string) string {
	for _, artifact := range artifacts {
		if artifact.Type == kind {
			return artifact.URL
		}
	}
	return ""
}
