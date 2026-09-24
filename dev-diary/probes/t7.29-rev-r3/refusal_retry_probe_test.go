// Review probe. Copy into internal/api/ of the reviewed worktree and run
//   go test -count=1 -run TestProbe ./internal/api/
// A mark done whose render never starts fails the episode. The answer and
// the error code docs promise a retry. Nothing moves a failed episode
// back to draft, so the promised retry answers 409. Either the retry must
// work, or neither the answer nor the docs may promise one.
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestProbeRenderRefusalPromisesNoDeadRetry(t *testing.T) {
	db, guests := openDiary(t)
	cookie, owner := mintGuest(t, guests)
	seedEpisodeRow(t, db, "ep-1", owner.ID, 1, "draft")
	stack := newEpisodeService(t, db, errBrokenStart)
	handler := NewEpisodes(stack.svc)
	done := func() *httptest.ResponseRecorder {
		return serve(guests, handler, cookie,
			httptest.NewRequest(http.MethodPost, "/api/episodes/ep-1/done", nil))
	}
	first := done()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode refusal: %v", err)
	}
	if first.Code != http.StatusServiceUnavailable || body.Error.Code != CodeRenderUnavailable {
		t.Fatalf("first done = %d %q, want 503 %q", first.Code, body.Error.Code, CodeRenderUnavailable)
	}
	retry := done()
	if retry.Code == http.StatusAccepted {
		return
	}
	src, err := os.ReadFile("respond.go")
	if err != nil {
		t.Fatalf("read respond.go: %v", err)
	}
	var promises []string
	if strings.Contains(strings.ToLower(body.Error.Message), "retry") {
		promises = append(promises, "the 503 message says "+body.Error.Message)
	}
	if strings.Contains(string(src), "explicit retry") {
		promises = append(promises, "respond.go says a failed episode waits for an explicit retry")
	}
	if len(promises) > 0 {
		t.Fatalf("the retry answers %d, so nothing retries, but %s", retry.Code, strings.Join(promises, ", and "))
	}
}
