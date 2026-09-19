package analysis

import (
	"context"
	"io"
	"sync"
	"time"
)

// ScriptedTranscriber replays one recorded batch exchange. It records
// the uploaded bytes and counts every call, then answers the canned
// completion from the single creation call. Tests pin the whole pass
// through it with no key and no network. The production wiring binds
// the Transcriber seam to the provider adapter with no change to the
// pass itself.
type ScriptedTranscriber struct {
	mu sync.Mutex
	// Completion answers Wait and pre-delete Get with every feature the
	// one creation call asked for.
	Completion TranscriptResult
	// Deleted answers Get after Delete with the deletion marker.
	Deleted TranscriptResult
	// UploadErr fails the upload the way an unreachable provider would.
	UploadErr error
	// CreateErr fails the creation call.
	CreateErr error
	// WaitErr fails the wait.
	WaitErr error
	// DeleteErr fails the delete call.
	DeleteErr error
	// GetErr fails the fetch.
	GetErr error
	// Uploads counts upload calls.
	Uploads int
	// Audio carries the last uploaded bytes.
	Audio []byte
	// Creates counts creation calls. The run asserts exactly one.
	Creates int
	// AudioURL carries the last creation audio URL.
	AudioURL string
	// Waits counts wait calls.
	Waits int
	// Deletes counts delete calls.
	Deletes int
	// DeleteIDs carries each deleted transcript id.
	DeleteIDs []string
	// Gets counts fetch calls.
	Gets int
}

// Upload records the audio bytes and returns a fixed upload URL.
func (s *ScriptedTranscriber) Upload(_ context.Context, audio io.Reader) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Uploads++
	if s.UploadErr != nil {
		return "", s.UploadErr
	}
	body, _ := io.ReadAll(audio)
	s.Audio = append([]byte(nil), body...)
	return "http://provider/audio/render", nil
}

// Create records the audio URL and returns the canned transcript id.
func (s *ScriptedTranscriber) Create(_ context.Context, req CreateRequest) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Creates++
	if s.CreateErr != nil {
		return "", s.CreateErr
	}
	s.AudioURL = req.AudioURL
	return "tx-scripted", nil
}

// Wait answers the canned completion.
func (s *ScriptedTranscriber) Wait(_ context.Context, _ string, _ time.Duration) (TranscriptResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Waits++
	if s.WaitErr != nil {
		return TranscriptResult{}, s.WaitErr
	}
	return s.Completion, nil
}

// Get answers the canned deletion once the transcript is deleted, and
// the completion before that.
func (s *ScriptedTranscriber) Get(_ context.Context, _ string) (TranscriptResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Gets++
	if s.GetErr != nil {
		return TranscriptResult{}, s.GetErr
	}
	if s.Deletes > 0 {
		return s.Deleted, nil
	}
	return s.Completion, nil
}

// Delete records one delete call.
func (s *ScriptedTranscriber) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Deletes++
	s.DeleteIDs = append(s.DeleteIDs, id)
	return s.DeleteErr
}

// ScriptedChapterer answers one canned chapter list and records the
// request the pass built. Tests run shaping offline through it. The
// production wiring binds the Chapterer seam to the chapter endpoint
// with no change to the pass itself.
type ScriptedChapterer struct {
	mu sync.Mutex
	// Drafts answers the call before shaping.
	Drafts []ChapterDraft
	// Usage books the call spend.
	Usage Usage
	// Raw carries the canned response bytes for the receipt store.
	Raw []byte
	// Err fails the call the way an unreachable provider would.
	Err error
	// Calls counts chapter calls.
	Calls int
	// Model carries the model id the pass asked for.
	Model string
	// Req carries the last chapter request.
	Req ChapterRequest
}

// CompleteChapters records the request and answers the canned drafts.
func (s *ScriptedChapterer) CompleteChapters(_ context.Context, model string, req ChapterRequest) (ChapterAnswer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Calls++
	s.Model = model
	s.Req = req
	if s.Err != nil {
		return ChapterAnswer{}, s.Err
	}
	return ChapterAnswer{Drafts: append([]ChapterDraft(nil), s.Drafts...), Usage: s.Usage, Raw: append([]byte(nil), s.Raw...)}, nil
}
