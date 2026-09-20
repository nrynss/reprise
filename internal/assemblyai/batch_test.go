package assemblyai_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nrynss/reprise/internal/assemblyai"
)

// scriptedBatch replays one recorded provider exchange. Each handler
// records its calls and answers from fixed bodies, so tests pin the wire
// shape without spending money.
type scriptedBatch struct {
	mu           sync.Mutex
	uploadHits   int
	uploadType   string
	uploadAuth   string
	uploadBody   []byte
	createHits   int
	createBody   map[string]any
	getHits      int
	getReplies   []string
	deleteHits   int
	deleteIDs    []string
	deleteStatus int
}

func (s *scriptedBatch) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/upload", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.uploadHits++
		s.uploadType = r.Header.Get("Content-Type")
		s.uploadAuth = r.Header.Get("Authorization")
		s.uploadBody = body
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"upload_url": "http://" + r.Host + "/audio/1"})
	})
	mux.HandleFunc("/v2/transcript", func(w http.ResponseWriter, r *http.Request) {
		var decoded map[string]any
		_ = json.NewDecoder(r.Body).Decode(&decoded)
		s.mu.Lock()
		s.createHits++
		s.createBody = decoded
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "tx-1", "status": "processing"})
	})
	mux.HandleFunc("/v2/transcript/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			s.mu.Lock()
			s.deleteHits++
			s.deleteIDs = append(s.deleteIDs, strings.TrimPrefix(r.URL.Path, "/v2/transcript/"))
			status := s.deleteStatus
			if status == 0 {
				status = http.StatusOK
			}
			s.mu.Unlock()
			w.WriteHeader(status)
			return
		}
		s.mu.Lock()
		hit := s.getHits
		s.getHits++
		reply := `{"id":"tx-1","status":"processing"}`
		if hit < len(s.getReplies) {
			reply = s.getReplies[hit]
		}
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, reply)
	})
	return mux
}

// completedBody is the recorded completion shape with two words, null
// speakers, and a billed duration.
const completedBody = `{"id":"tx-1","status":"completed",` +
	`"text":"The harbour lantern.", "audio_duration": 19, "confidence": 0.99,` +
	`"words":[{"text":"The","start":32,"end":112,"confidence":0.92,"speaker":null},` +
	`{"text":"harbour","start":200,"end":430,"confidence":0.97,"speaker":null}]}`

// deletedBody is the recorded fetch after delete with scrubbed fields.
const deletedBody = `{"id":"tx-1","status":"completed",` +
	`"text":"Deleted by user.", "audio_url":"http://deleted_by_user",` +
	`"confidence":null, "words":null}`

// recordedModelsError is the exact provider message from a rejected batch
// creation. The provider names every accepted id inside it, so the pin test
// compares the code list against this shape.
const recordedModelsError = `"speech_models" must be a non-empty list containing one or more of: "universal-3-pro", "universal-2", "universal-3-5-pro"`

// TestAcceptedModelsMatchRecordedShape pins the accepted set against the
// recorded provider rejection. A provider rename changes the recorded ids,
// which fails this test until the code list follows.
func TestAcceptedModelsMatchRecordedShape(t *testing.T) {
	t.Parallel()
	var recorded []string
	for _, field := range strings.Split(recordedModelsError, `"`) {
		if strings.HasPrefix(field, "universal-") {
			recorded = append(recorded, field)
		}
	}
	accepted := assemblyai.AcceptedBatchModels()
	if len(recorded) != len(accepted) {
		t.Fatalf("recorded ids = %v, accepted = %v, want the same set", recorded, accepted)
	}
	seen := map[string]bool{}
	for _, id := range accepted {
		seen[id] = true
	}
	for _, id := range recorded {
		if !seen[id] {
			t.Fatalf("recorded id %q is not accepted, update the accepted set", id)
		}
	}
	if !assemblyai.ValidBatchModel(assemblyai.BatchModel) {
		t.Fatalf("default model %q is not accepted", assemblyai.BatchModel)
	}
}

// TestValidBatchModel checks the validator accepts the wire id and the
// settings spelling and rejects a renamed id.
func TestValidBatchModel(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"universal-3-5-pro", "universal-3.5-pro", "universal-3-pro", "universal-2"} {
		if !assemblyai.ValidBatchModel(id) {
			t.Fatalf("ValidBatchModel(%q) = false, want true", id)
		}
	}
	if assemblyai.ValidBatchModel("universal-9-pro") {
		t.Fatal("ValidBatchModel(renamed) = true, want false")
	}
}

// TestCreateSendsWireIDForSettingsSpelling checks the dotted settings value
// reaches the provider as the dashed id it accepts.
func TestCreateSendsWireIDForSettingsSpelling(t *testing.T) {
	t.Parallel()
	fake := &scriptedBatch{}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client, err := assemblyai.NewBatchClient(assemblyai.Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   "universal-3.5-pro",
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if _, err := client.Create(t.Context(), assemblyai.CreateRequest{AudioURL: server.URL + "/audio/1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := client.Model(); got != "universal-3-5-pro" {
		t.Fatalf("client model = %q, want the accepted wire id", got)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	models, _ := fake.createBody["speech_models"].([]any)
	if len(models) != 1 || models[0] != "universal-3-5-pro" {
		t.Fatalf("speech_models = %v, want the accepted wire id", fake.createBody["speech_models"])
	}
}

// TestCreateSurfacesRecordedRejection checks a renamed model id fails with
// the request sentinel carrying the recorded provider text.
func TestCreateSurfacesRecordedRejection(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var decoded struct {
			SpeechModels []string `json:"speech_models"`
		}
		_ = json.NewDecoder(r.Body).Decode(&decoded)
		ok := false
		for _, id := range decoded.SpeechModels {
			if assemblyai.ValidBatchModel(id) {
				ok = true
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": recordedModelsError})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "tx-1", "status": "processing"})
	}))
	defer server.Close()
	client, err := assemblyai.NewBatchClient(assemblyai.Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   "universal-9-pro",
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	_, err = client.Create(t.Context(), assemblyai.CreateRequest{AudioURL: server.URL + "/audio/1"})
	if !errors.Is(err, assemblyai.ErrBatchRequest) {
		t.Fatalf("create error = %v, want ErrBatchRequest", err)
	}
	for _, want := range []string{"speech_models", "universal-3-pro", "universal-2", "universal-3-5-pro"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("create error = %v, want %q inside", err, want)
		}
	}
}

// openClient points one test client at the scripted server.
func openClient(t *testing.T, server *httptest.Server) *assemblyai.BatchClient {
	t.Helper()
	client, err := assemblyai.NewBatchClient(assemblyai.Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return client
}

// TestUploadPostsOctetStream checks the upload carries raw bytes with the
// key attached and returns the provider URL.
func TestUploadPostsOctetStream(t *testing.T) {
	t.Parallel()
	fake := &scriptedBatch{}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := openClient(t, server)
	url, err := client.Upload(t.Context(), strings.NewReader("fake-wav-bytes"))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if !strings.HasPrefix(url, server.URL+"/audio/") {
		t.Fatalf("upload url = %q, want the server audio path", url)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.uploadHits != 1 {
		t.Fatalf("upload hits = %d, want 1", fake.uploadHits)
	}
	if fake.uploadType != "application/octet-stream" {
		t.Fatalf("upload type = %q, want octet-stream", fake.uploadType)
	}
	if fake.uploadAuth == "" {
		t.Fatal("upload carries no authorization header")
	}
	if string(fake.uploadBody) != "fake-wav-bytes" {
		t.Fatalf("upload body = %q, want the raw bytes", fake.uploadBody)
	}
}

// TestCreateSendsModelAndKeyterms checks one creation call carries the
// flagship model and the mention keyterms together.
func TestCreateSendsModelAndKeyterms(t *testing.T) {
	t.Parallel()
	fake := &scriptedBatch{}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := openClient(t, server)
	id, err := client.Create(t.Context(), assemblyai.CreateRequest{
		AudioURL: server.URL + "/audio/1",
		Keyterms: []string{"Mara", "Quilby"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id != "tx-1" {
		t.Fatalf("create id = %q, want tx-1", id)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	models, _ := fake.createBody["speech_models"].([]any)
	if len(models) != 1 || models[0] != "universal-3-5-pro" {
		t.Fatalf("speech_models = %v, want the flagship model", fake.createBody["speech_models"])
	}
	terms, _ := fake.createBody["keyterms_prompt"].([]any)
	if len(terms) != 2 || terms[0] != "Mara" || terms[1] != "Quilby" {
		t.Fatalf("keyterms_prompt = %v, want both mention names", fake.createBody["keyterms_prompt"])
	}
}

// TestWaitPollsUntilCompleted replays two processing polls then the
// recorded completion and pins the parsed words.
func TestWaitPollsUntilCompleted(t *testing.T) {
	t.Parallel()
	fake := &scriptedBatch{getReplies: []string{
		`{"id":"tx-1","status":"processing"}`,
		`{"id":"tx-1","status":"queued"}`,
		completedBody,
	}}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := openClient(t, server)
	tx, err := client.Wait(t.Context(), "tx-1", time.Millisecond)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if tx.Status != "completed" || len(tx.Words) != 2 {
		t.Fatalf("transcript = %+v, want completed with two words", tx)
	}
	first := tx.Words[0]
	if first.Text != "The" || first.StartMs != 32 || first.EndMs != 112 {
		t.Fatalf("first word = %+v, want The at 32 to 112", first)
	}
	if tx.AudioDurationSecs != 19 {
		t.Fatalf("audio duration = %d, want 19", tx.AudioDurationSecs)
	}
	if len(tx.Raw) == 0 || !strings.Contains(string(tx.Raw), "audio_duration") {
		t.Fatal("raw receipt is empty or misses the recorded body")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.getHits != 3 {
		t.Fatalf("get hits = %d, want 3 polls", fake.getHits)
	}
}

// TestWaitReportsProviderError replays a failed transcript and checks the
// sentinel wraps the provider text.
func TestWaitReportsProviderError(t *testing.T) {
	t.Parallel()
	fake := &scriptedBatch{getReplies: []string{
		`{"id":"tx-1","status":"error","error":"audio too short"}`,
	}}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := openClient(t, server)
	_, err := client.Wait(t.Context(), "tx-1", time.Millisecond)
	if !errors.Is(err, assemblyai.ErrBatchFailed) {
		t.Fatalf("wait error = %v, want ErrBatchFailed", err)
	}
	if !strings.Contains(err.Error(), "audio too short") {
		t.Fatalf("wait error = %v, want the provider text inside", err)
	}
}

// TestDeleteConfirmedByGet deletes the transcript and proves the follow-up
// fetch reads the deletion marker with no words.
func TestDeleteConfirmedByGet(t *testing.T) {
	t.Parallel()
	fake := &scriptedBatch{getReplies: []string{deletedBody}}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := openClient(t, server)
	if err := client.Delete(t.Context(), "tx-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	after, err := client.Get(t.Context(), "tx-1")
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if !after.Deleted() {
		t.Fatalf("transcript after delete = %+v, want the deletion marker", after)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.deleteHits != 1 || len(fake.deleteIDs) != 1 || fake.deleteIDs[0] != "tx-1" {
		t.Fatalf("delete calls = %v, want one for tx-1", fake.deleteIDs)
	}
}

// TestDeleteFailureSurfaces checks a non-200 delete returns the request
// sentinel instead of reading as success.
func TestDeleteFailureSurfaces(t *testing.T) {
	t.Parallel()
	fake := &scriptedBatch{deleteStatus: http.StatusNotFound}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := openClient(t, server)
	if err := client.Delete(t.Context(), "tx-1"); !errors.Is(err, assemblyai.ErrBatchRequest) {
		t.Fatalf("delete error = %v, want ErrBatchRequest", err)
	}
}

// TestClientRejectsEmptyKey checks the constructor refuses an empty key
// with its sentinel and no HTTP call.
func TestClientRejectsEmptyKey(t *testing.T) {
	t.Parallel()
	if _, err := assemblyai.NewBatchClient(assemblyai.Config{}); !errors.Is(err, assemblyai.ErrBatchInvalid) {
		t.Fatalf("new client error = %v, want ErrBatchInvalid", err)
	}
}

// TestWaitHonoursCancel checks a cancelled context ends the poll with the
// context error rather than hanging on the provider.
func TestWaitHonoursCancel(t *testing.T) {
	t.Parallel()
	fake := &scriptedBatch{}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := openClient(t, server)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := client.Wait(ctx, "tx-1", time.Millisecond)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("wait error = %v, want context.Canceled", err)
	}
}
