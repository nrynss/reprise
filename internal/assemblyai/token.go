// Package assemblyai talks to AssemblyAI from the server only. The key
// never reaches the browser. Every paid call reserves budget first.
package assemblyai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// TokenBaseURL is the provider host that mints single-use session tokens.
const TokenBaseURL = "https://agents.assemblyai.com"

// TokenExpirySeconds is how long a minted token stays usable before first use.
const TokenExpirySeconds = 60

// TokenCapMinSeconds and TokenCapMaxSeconds bound the session cap the mint
// request may carry. The provider ends the session at the cap even when the
// browser vanishes.
const TokenCapMinSeconds = 60
const TokenCapMaxSeconds = 10800

// ErrInvalid reports a client or mint argument the package cannot honour,
// such as an empty key or a cap outside the provider bounds.
var ErrInvalid = errors.New("assemblyai: invalid argument")

// ErrMint reports a token request the provider refused or answered badly.
// The wrapped message names the failure without ever carrying the key.
var ErrMint = errors.New("assemblyai: mint token")

// tokenResponse decodes the mint reply. The reply carries only the token,
// so nothing secret can leak through it.
type tokenResponse struct {
	// Token is the single-use token the socket dials with.
	Token string `json:"token"`
}

// Client mints single-use session tokens over plain HTTPS. Create it with
// NewClient, because the zero value has no key and no transport. A Client
// is safe for concurrent use.
type Client struct {
	base string
	key  string
	http *http.Client
}

// NewClient returns a Client that mints against baseURL with apiKey. A nil
// transport becomes a client with a 30 second timeout, so no mint blocks
// past the process budget for one request.
func NewClient(baseURL, apiKey string, transport *http.Client) (*Client, error) {
	if baseURL == "" || apiKey == "" {
		return nil, fmt.Errorf("assemblyai: new client: %w: base URL and API key must not be empty", ErrInvalid)
	}
	if transport == nil {
		transport = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{base: strings.TrimSuffix(baseURL, "/"), key: apiKey, http: transport}, nil
}

// Mint asks the provider for one single-use token capped at
// maxSessionSeconds. The token expires 60 seconds after minting when unused,
// and the provider ends the session at the cap. Mint returns ErrMint when
// the provider refuses or answers badly, and ErrInvalid for a cap outside
// 60 to 10800 seconds.
func (c *Client) Mint(ctx context.Context, maxSessionSeconds int) (string, error) {
	if maxSessionSeconds < TokenCapMinSeconds || maxSessionSeconds > TokenCapMaxSeconds {
		return "", fmt.Errorf("assemblyai: mint cap %d: %w: cap must sit within 60 to 10800 seconds", maxSessionSeconds, ErrInvalid)
	}
	query := url.Values{}
	query.Set("expires_in_seconds", strconv.Itoa(TokenExpirySeconds))
	query.Set("max_session_duration_seconds", strconv.Itoa(maxSessionSeconds))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/v1/token?"+query.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("assemblyai: mint token request: %w", ErrMint)
	}
	req.Header.Set("Authorization", c.key)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("assemblyai: mint token call: %w", ErrMint)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("assemblyai: mint token reply: %w", ErrMint)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("assemblyai: mint token status %d: %w", resp.StatusCode, ErrMint)
	}
	var decoded tokenResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", fmt.Errorf("assemblyai: mint token reply: %w", ErrMint)
	}
	if decoded.Token == "" {
		return "", fmt.Errorf("assemblyai: mint token empty reply: %w", ErrMint)
	}
	return decoded.Token, nil
}
