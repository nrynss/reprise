package assemblyai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// BatchBaseURL is the production AssemblyAI API origin.
const BatchBaseURL = "https://api.assemblyai.com"

// BatchModel names the batch model the edit pass transcribes with.
const BatchModel = "universal-3-5-pro"

// acceptedBatchModels lists the speech model ids the provider accepts. The
// set mirrors the ids the provider names in its recorded 400 reply. Update
// this list when a fresh rejection names a different set.
var acceptedBatchModels = []string{"universal-3-pro", "universal-2", "universal-3-5-pro"}

// AcceptedBatchModels returns the speech model ids the provider accepts. The
// caller compares a fresh provider rejection against this list and updates
// the list when the provider renames a model.
func AcceptedBatchModels() []string {
	return append([]string(nil), acceptedBatchModels...)
}

// ValidBatchModel reports whether the provider accepts the model id. It maps
// the settings spelling first, so the dotted marketing name counts as valid.
func ValidBatchModel(model string) bool {
	want := normalizeBatchModel(model)
	for _, accepted := range acceptedBatchModels {
		if want == accepted {
			return true
		}
	}
	return false
}

// normalizeBatchModel maps the settings spelling to the wire id. The settings
// file spells the flagship model with a dot. The provider accepts dashes
// only. Unknown ids pass through unchanged, so the provider reports them
// with its reason.
func normalizeBatchModel(model string) string {
	if model == "universal-3.5-pro" {
		return BatchModel
	}
	return model
}

// BatchDollarsPerHour prices one audio hour on the batch model.
const BatchDollarsPerHour = 0.21

// DeletedText is the text a fetched transcript carries after deletion.
const DeletedText = "Deleted by user."

// ErrBatchInvalid reports a client call with an empty id, URL, or key.
var ErrBatchInvalid = errors.New("assemblyai: invalid batch argument")

// ErrBatchRequest reports a batch HTTP call that failed or answered badly.
var ErrBatchRequest = errors.New("assemblyai: batch request failed")

// ErrBatchFailed reports a transcript the provider marked failed.
var ErrBatchFailed = errors.New("assemblyai: transcription failed")

// ErrBatchDelete reports a provider copy that stays readable after delete.
var ErrBatchDelete = errors.New("assemblyai: transcript still readable")

// Word is one transcribed word with millisecond offsets on the uploaded audio.
type Word struct {
	// Text is the word as heard, with its original casing.
	Text string
	// StartMs is the word start in milliseconds from the audio start.
	StartMs int64
	// EndMs is the word end in milliseconds from the audio start.
	EndMs int64
	// Confidence scores the word from zero to one.
	Confidence float64
}

// Transcript is one batch transcript. Raw carries the exact bytes the
// provider returned, so the caller persists the response before deleting
// the provider copy.
type Transcript struct {
	// ID is the provider transcript id.
	ID string
	// Status is the provider status, such as queued or completed.
	Status string
	// Text is the full transcript text, or the deletion marker.
	Text string
	// AudioDurationSecs is the billed audio length in seconds.
	AudioDurationSecs int64
	// Words holds every word with its timing, or nothing after deletion.
	Words []Word
	// Failure carries the provider error text on a failed transcript.
	Failure string
	// Raw is the full GET response body for the receipt store.
	Raw []byte
}

// Deleted reports whether the transcript reads as provider-deleted. Delete
// scrubs the content but keeps the row, so success looks like the marker
// text with no words rather than a missing row.
func (t Transcript) Deleted() bool {
	return t.Text == DeletedText && len(t.Words) == 0
}

// CreateRequest starts one batch transcription of an uploaded file.
type CreateRequest struct {
	// AudioURL is the upload URL the upload call returned.
	AudioURL string
	// Keyterms boosts names from past episodes, most recent first.
	Keyterms []string
}

// createBody is the wire shape the create call sends. Keyterms stay out
// when empty, so the JSON matches the recorded shape either way.
type createBody struct {
	// AudioURL is the upload URL the upload call returned.
	AudioURL string `json:"audio_url"`
	// SpeechModels carries the single transcription model for this pass.
	SpeechModels []string `json:"speech_models"`
	// Keyterms boosts names from past episodes, most recent first.
	Keyterms []string `json:"keyterms_prompt,omitempty"`
}

// Config carries the batch client settings. The caller loads the key from
// settings and passes the value, so this package never reads the environment.
type Config struct {
	// BaseURL overrides the API origin. Tests point it at a fake server.
	BaseURL string
	// APIKey authorizes every call. It never leaves the server.
	APIKey string
	// Model overrides the transcription model. Empty means BatchModel. The
	// dotted settings spelling maps to the dashed wire id.
	Model string
	// Client overrides the HTTP client. Empty means a 90 second client.
	Client *http.Client
}

// BatchClient talks to the AssemblyAI batch API over plain HTTP. Create one
// with NewBatchClient, because the zero value carries no key.
type BatchClient struct {
	base  string
	key   string
	model string
	api   *http.Client
}

// NewBatchClient validates cfg and returns the client. It defaults the origin,
// the model, and the HTTP client, so callers override only what tests need.
func NewBatchClient(cfg Config) (*BatchClient, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("assemblyai: new batch client: %w: empty key", ErrBatchInvalid)
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = BatchBaseURL
	}
	model := normalizeBatchModel(cfg.Model)
	if model == "" {
		model = BatchModel
	}
	api := cfg.Client
	if api == nil {
		api = &http.Client{Timeout: 90 * time.Second}
	}
	return &BatchClient{base: base, key: cfg.APIKey, model: model, api: api}, nil
}

// Model returns the transcription model this client creates with.
func (c *BatchClient) Model() string { return c.model }

// request sends one JSON call and returns the raw body. It sets the key on
// every call and reports provider error text without matching on it.
func (c *BatchClient) request(ctx context.Context, method, path string, body any) ([]byte, error) {
	if c == nil || c.api == nil || c.key == "" {
		return nil, fmt.Errorf("assemblyai: batch call: %w: client not built", ErrBatchInvalid)
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("assemblyai: batch call: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return nil, fmt.Errorf("assemblyai: batch call: %w", err)
	}
	req.Header.Set("Authorization", c.key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.api.Do(req)
	if err != nil {
		return nil, fmt.Errorf("assemblyai: batch call %s %s: %w: %w", method, path, err, ErrBatchRequest)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("assemblyai: batch call %s %s: %w", method, path, err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("assemblyai: batch call %s %s status %d: %.300s: %w",
			method, path, resp.StatusCode, raw, ErrBatchRequest)
	}
	return raw, nil
}

// Upload posts raw audio bytes and returns the URL the create call reads.
// The caller uploads the user stem only, never the render.
func (c *BatchClient) Upload(ctx context.Context, audio io.Reader) (string, error) {
	if audio == nil {
		return "", fmt.Errorf("assemblyai: upload: %w: nil audio", ErrBatchInvalid)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.base+"/v2/upload", audio)
	if err != nil {
		return "", fmt.Errorf("assemblyai: upload: %w", err)
	}
	req.Header.Set("Authorization", c.key)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.api.Do(req)
	if err != nil {
		return "", fmt.Errorf("assemblyai: upload: %w: %w", err, ErrBatchRequest)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("assemblyai: upload: %w", err)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("assemblyai: upload status %d: %.300s: %w",
			resp.StatusCode, raw, ErrBatchRequest)
	}
	var decoded struct {
		UploadURL string `json:"upload_url"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", fmt.Errorf("assemblyai: upload: %w", err)
	}
	if decoded.UploadURL == "" {
		return "", fmt.Errorf("assemblyai: upload: %w: empty upload url", ErrBatchRequest)
	}
	return decoded.UploadURL, nil
}

// Create starts one transcription and returns its id. Keyterms carry the
// recurring names from past episodes. The status starts queued, so the
// caller follows with Wait.
func (c *BatchClient) Create(ctx context.Context, req CreateRequest) (string, error) {
	if strings.TrimSpace(req.AudioURL) == "" {
		return "", fmt.Errorf("assemblyai: create: %w: empty audio url", ErrBatchInvalid)
	}
	body := createBody{
		AudioURL:     req.AudioURL,
		SpeechModels: []string{c.model},
	}
	if len(req.Keyterms) > 0 {
		body.Keyterms = req.Keyterms
	}
	raw, err := c.request(ctx, "POST", "/v2/transcript", body)
	if err != nil {
		return "", err
	}
	var decoded struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", fmt.Errorf("assemblyai: create: %w", err)
	}
	if decoded.ID == "" {
		return "", fmt.Errorf("assemblyai: create: %w: empty transcript id", ErrBatchRequest)
	}
	return decoded.ID, nil
}

// transcriptBody mirrors the GET shape the probe recorded, with the raw
// kept for the receipt store.
type transcriptBody struct {
	ID            string  `json:"id"`
	Status        string  `json:"status"`
	Text          *string `json:"text"`
	AudioDuration *int64  `json:"audio_duration"`
	Failure       *string `json:"error"`
	Words         []struct {
		Text       string  `json:"text"`
		Start      int64   `json:"start"`
		End        int64   `json:"end"`
		Confidence float64 `json:"confidence"`
	} `json:"words"`
}

// Get fetches one transcript and keeps the raw body beside the parse. The
// caller persists Raw on receipt, before any delete.
func (c *BatchClient) Get(ctx context.Context, id string) (Transcript, error) {
	if strings.TrimSpace(id) == "" {
		return Transcript{}, fmt.Errorf("assemblyai: get: %w: empty id", ErrBatchInvalid)
	}
	raw, err := c.request(ctx, "GET", "/v2/transcript/"+id, nil)
	if err != nil {
		return Transcript{}, err
	}
	var decoded transcriptBody
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return Transcript{}, fmt.Errorf("assemblyai: get %s: %w", id, err)
	}
	out := Transcript{ID: decoded.ID, Status: decoded.Status, Raw: raw}
	if decoded.Text != nil {
		out.Text = *decoded.Text
	}
	if decoded.AudioDuration != nil {
		out.AudioDurationSecs = *decoded.AudioDuration
	}
	if decoded.Failure != nil {
		out.Failure = *decoded.Failure
	}
	for _, w := range decoded.Words {
		out.Words = append(out.Words, Word{
			Text:       w.Text,
			StartMs:    w.Start,
			EndMs:      w.End,
			Confidence: w.Confidence,
		})
	}
	return out, nil
}

// Wait polls one transcript until it completes or fails. Polling carries
// results because webhooks never reach this machine. The context bounds the
// wait, and the interval sets the poll gap. A zero interval polls every
// five seconds.
func (c *BatchClient) Wait(ctx context.Context, id string, interval time.Duration) (Transcript, error) {
	if strings.TrimSpace(id) == "" {
		return Transcript{}, fmt.Errorf("assemblyai: wait: %w: empty id", ErrBatchInvalid)
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		tx, err := c.Get(ctx, id)
		if err != nil {
			return Transcript{}, err
		}
		switch tx.Status {
		case "completed":
			return tx, nil
		case "error":
			return Transcript{}, fmt.Errorf("assemblyai: wait %s: %s: %w", id, tx.Failure, ErrBatchFailed)
		}
		select {
		case <-ctx.Done():
			return Transcript{}, fmt.Errorf("assemblyai: wait %s: %w", id, ctx.Err())
		case <-timer.C:
			timer.Reset(interval)
		}
	}
}

// Delete removes one transcript by id. Deletion is soft on the provider
// side, so the caller confirms with Get and Deleted before settling.
func (c *BatchClient) Delete(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("assemblyai: delete: %w: empty id", ErrBatchInvalid)
	}
	if _, err := c.request(ctx, "DELETE", "/v2/transcript/"+id, nil); err != nil {
		return err
	}
	return nil
}
