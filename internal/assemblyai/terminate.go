// Session records beside one Voice Agent session live here. Fetching reads
// the record, terminating deletes it. Deleting removes the record while a
// connected socket stays open and billable, so deletion never stands in
// for ending the call. The caller ends the socket first, then deletes.
package assemblyai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// SessionsBaseURL is the provider host that keeps the session records.
const SessionsBaseURL = "https://agents.assemblyai.com"

// Sentinels, one per failure condition.
var (
	// ErrFetch reports a session record read that failed.
	ErrFetch = errors.New("assemblyai: fetch session")
	// ErrTerminate reports a session record delete that failed.
	ErrTerminate = errors.New("assemblyai: terminate session")
	// ErrSessionGone reports a session record the provider no longer
	// keeps. A fetch meets it on 404. A delete treats it as success,
	// because ending an ended session stays safe.
	ErrSessionGone = errors.New("assemblyai: session is gone")
)

// SessionArtifact is one file the provider keeps beside a session, such as
// the stereo recording or the timeline. URLs expire, so the caller
// persists the bytes on receipt.
type SessionArtifact struct {
	// Type names the artifact, such as audio or timeline.
	Type string `json:"type"`
	// URL is the expiring download URL. It needs no auth header.
	URL string `json:"url"`
	// ContentType is the media type of the bytes behind the URL.
	ContentType string `json:"content_type"`
}

// SessionStatus is one session record read. DurationSeconds stays nil
// while the session is open, so callers bill open sessions from CreatedAt
// and never from a missing number.
type SessionStatus struct {
	// ID is the provider session id that was read.
	ID string `json:"id"`
	// Status is the provider lifecycle state, such as created or
	// completed.
	Status string `json:"status"`
	// CloseReason is why the session closed, or empty while open.
	CloseReason string `json:"public_close_reason"`
	// DurationSeconds is the connected time in seconds, or nil while the
	// session stays open.
	DurationSeconds *float64 `json:"duration_seconds"`
	// CreatedAt is when the provider opened the session.
	CreatedAt time.Time `json:"created_at"`
	// EndedAt is when the provider closed the session, or nil while open.
	EndedAt *time.Time `json:"ended_at"`
	// Artifacts are the files the provider keeps beside the session.
	// Open sessions carry none.
	Artifacts []SessionArtifact `json:"artifacts"`
}

// TerminateResult is one session record delete.
type TerminateResult struct {
	// Deleted reports the provider removed the record on this call. False
	// means the record was already gone, which still counts as success.
	Deleted bool `json:"deleted"`
}

// SessionsClient reads and deletes provider session records over plain
// HTTPS. Create it with NewSessionsClient, because the zero value has no
// key and no transport. A SessionsClient is safe for concurrent use.
type SessionsClient struct {
	base string
	key  string
	http *http.Client
}

// NewSessionsClient returns a SessionsClient that reads against baseURL
// with apiKey. A nil transport becomes a client with a 30 second timeout,
// so no record call blocks past the process budget for one request.
func NewSessionsClient(baseURL, apiKey string, transport *http.Client) (*SessionsClient, error) {
	if baseURL == "" || apiKey == "" {
		return nil, fmt.Errorf("assemblyai: new sessions client: %w: base URL and API key must not be empty", ErrInvalid)
	}
	if transport == nil {
		transport = &http.Client{Timeout: 30 * time.Second}
	}
	return &SessionsClient{base: strings.TrimSuffix(baseURL, "/"), key: apiKey, http: transport}, nil
}

// FetchSession returns the provider record for a session id. A record the
// provider no longer keeps reports ErrSessionGone, so the caller settles
// from what it already knows instead of retrying a read that stays 404.
func (c *SessionsClient) FetchSession(ctx context.Context, sessionID string) (SessionStatus, error) {
	if sessionID == "" {
		return SessionStatus{}, fmt.Errorf("assemblyai: fetch session: %w: session id must not be empty", ErrInvalid)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/v1/sessions/"+sessionID, nil)
	if err != nil {
		return SessionStatus{}, fmt.Errorf("assemblyai: fetch session request: %w", ErrFetch)
	}
	req.Header.Set("Authorization", c.key)
	resp, err := c.http.Do(req)
	if err != nil {
		return SessionStatus{}, fmt.Errorf("assemblyai: fetch session call: %w", ErrFetch)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return SessionStatus{}, fmt.Errorf("assemblyai: fetch session reply: %w", ErrFetch)
	}
	if resp.StatusCode == http.StatusNotFound {
		return SessionStatus{}, fmt.Errorf("assemblyai: fetch session %s: %w", sessionID, ErrSessionGone)
	}
	if resp.StatusCode != http.StatusOK {
		return SessionStatus{}, fmt.Errorf("assemblyai: fetch session status %d: %w", resp.StatusCode, ErrFetch)
	}
	var decoded SessionStatus
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return SessionStatus{}, fmt.Errorf("assemblyai: fetch session reply: %w", ErrFetch)
	}
	if decoded.ID == "" {
		return SessionStatus{}, fmt.Errorf("assemblyai: fetch session empty reply: %w", ErrFetch)
	}
	return decoded, nil
}

// TerminateSession deletes the provider record for a session id. Deleting
// removes the record while a connected socket stays open and billable, so
// the caller settles first and deletes after. A record that is already
// gone reports success with Deleted false, because ending an ended
// session stays safe.
func (c *SessionsClient) TerminateSession(ctx context.Context, sessionID string) (TerminateResult, error) {
	if sessionID == "" {
		return TerminateResult{}, fmt.Errorf("assemblyai: terminate session: %w: session id must not be empty", ErrInvalid)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.base+"/v1/sessions/"+sessionID, nil)
	if err != nil {
		return TerminateResult{}, fmt.Errorf("assemblyai: terminate session request: %w", ErrTerminate)
	}
	req.Header.Set("Authorization", c.key)
	resp, err := c.http.Do(req)
	if err != nil {
		return TerminateResult{}, fmt.Errorf("assemblyai: terminate session call: %w", ErrTerminate)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		return TerminateResult{Deleted: false}, nil
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return TerminateResult{}, fmt.Errorf("assemblyai: terminate session status %d: %w", resp.StatusCode, ErrTerminate)
	}
	return TerminateResult{Deleted: true}, nil
}
