package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nrynss/keel/ffmpeg"
	"github.com/nrynss/keel/mediastore"
	mediasqlitestore "github.com/nrynss/keel/mediastore/sqlitestore"
	keelsqlite "github.com/nrynss/keel/sqlite"
	"github.com/nrynss/keel/upload"
	"github.com/nrynss/reprise/internal/api"
	"github.com/nrynss/reprise/internal/broker"
	"github.com/nrynss/reprise/internal/episode"
	"github.com/nrynss/reprise/internal/identity"
	reprisestore "github.com/nrynss/reprise/internal/store"
	"github.com/nrynss/reprise/internal/transcript"
)

// sineRawPCM returns headerless signed 16 bit mono sine samples at rate
// hertz. The browser posts raw PCM with no RIFF header, so the pins use
// these bytes to prove the server heads them from the stored rate.
func sineRawPCM(rate int, secs float64) []byte {
	total := int(float64(rate) * secs)
	out := make([]byte, 0, total*2)
	var sample [2]byte
	for i := range total {
		v := int16(10000 * math.Sin(2*math.Pi*440*float64(i)/float64(rate)))
		binary.LittleEndian.PutUint16(sample[:], uint16(v))
		out = append(out, sample[:]...)
	}
	return out
}

// persistRawStem stores one raw PCM stem for owner and returns its blob id.
func persistRawStem(t *testing.T, fx *wireFixture, owner, group string, body []byte) string {
	t.Helper()
	id, err := fx.media.Persist(t.Context(), bytes.NewReader(body), mediastore.Put{
		ContentType: "audio/pcm",
		Owner:       owner,
		Group:       group,
		Visibility:  mediastore.Private,
	})
	if err != nil {
		t.Fatalf("persist raw stem: %v", err)
	}
	return id
}

// linkStemRowsAtRate stores both stem rows at one sample rate for one
// episode and fails the test on error.
func linkStemRowsAtRate(t *testing.T, fx *wireFixture, owner, episodeID, userMedia, hostMedia string, rate int64) {
	t.Helper()
	rows := []struct{ role, media string }{
		{transcript.RoleUser, userMedia},
		{transcript.RoleHost, hostMedia},
	}
	for _, row := range rows {
		if _, err := fx.db.Writer().ExecContext(t.Context(),
			`INSERT INTO stems (id, owner_id, episode_id, media_id, role, sample_rate, start_offset_ms)
			 VALUES (?, ?, ?, ?, ?, ?, 0)`,
			"stem-"+episodeID+"-"+row.role, owner, episodeID, row.media, row.role, rate); err != nil {
			t.Fatalf("link %s stem: %v", row.role, err)
		}
	}
}

// openUploadMux wires the real upload handler behind the session owner
// wrapper and the media store behind the refusal header, the way the
// binary mounts them.
func openUploadMux(t *testing.T) (*http.ServeMux, *keelsqlite.DB) {
	t.Helper()
	ctx := t.Context()
	db, err := keelsqlite.Open(ctx, keelsqlite.Config{Path: filepath.Join(t.TempDir(), "upload-pcm.db")})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := reprisestore.Open(ctx, db); err != nil {
		t.Fatalf("migrate diary schema: %v", err)
	}
	identitySvc, err := identity.New(ctx, identity.Config{DB: db, SigningKey: "upload-pcm-test-signing-key"})
	if err != nil {
		t.Fatalf("open identity: %v", err)
	}
	mediaDir := t.TempDir()
	mediaIndex, err := mediasqlitestore.Open(ctx, mediasqlitestore.Config{DB: db})
	if err != nil {
		t.Fatalf("open media index: %v", err)
	}
	media, err := mediastore.Open(ctx, mediastore.Config{
		Dir:          mediaDir,
		Index:        mediaIndex,
		ContentTypes: mediaContentTypes,
		Authorize:    identitySvc.AuthorizeMedia,
	})
	if err != nil {
		t.Fatalf("open media store: %v", err)
	}
	uploads, err := upload.New(upload.Config{
		Dir:      filepath.Join(t.TempDir(), "stage"),
		Store:    media,
		BasePath: api.UploadBasePath,
	})
	if err != nil {
		t.Fatalf("open upload handler: %v", err)
	}
	t.Cleanup(func() { _ = uploads.Close() })
	mux := http.NewServeMux()
	mux.Handle(api.UploadBasePath, identitySvc.Middleware(withUploadOwner(uploads)))
	mux.Handle(api.UploadBasePath+"/", identitySvc.Middleware(withUploadOwner(uploads)))
	mux.Handle("GET /media/{id}", identitySvc.Middleware(withMediaRefusalHeader(media)))
	return mux, db
}

// openUpload starts one upload of the named type and returns its id with
// the guest cookie the open minted.
func openUpload(t *testing.T, mux *http.ServeMux, contentType string) (string, *http.Cookie) {
	t.Helper()
	openReq := httptest.NewRequest(http.MethodPost, api.UploadBasePath,
		bytes.NewReader([]byte(`{"owner":"guest","content_type":"`+contentType+`","visibility":"private"}`)))
	openRec := httptest.NewRecorder()
	mux.ServeHTTP(openRec, openReq)
	if openRec.Code != http.StatusCreated {
		t.Fatalf("open %s status = %d, want 201: %s", contentType, openRec.Code, openRec.Body.String())
	}
	var opened struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(openRec.Body.Bytes(), &opened); err != nil {
		t.Fatalf("decode open response: %v", err)
	}
	var cookie *http.Cookie
	for _, c := range openRec.Result().Cookies() {
		if c.Name == identity.CookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("open set no session cookie")
	}
	return opened.ID, cookie
}

// completeUpload sends one chunk and completes the upload, then reads the
// stored bytes back as the owner.
func completeUpload(t *testing.T, mux *http.ServeMux, id string, cookie *http.Cookie, payload []byte) []byte {
	t.Helper()
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	putReq := httptest.NewRequest(http.MethodPut, api.UploadBasePath+"/"+id+"/chunks/0", bytes.NewReader(payload))
	putReq.Header.Set("X-Chunk-SHA256", digest)
	putReq.AddCookie(cookie)
	putRec := httptest.NewRecorder()
	mux.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("chunk status = %d, want 200: %s", putRec.Code, putRec.Body.String())
	}
	completeReq := httptest.NewRequest(http.MethodPost, api.UploadBasePath+"/"+id+"/complete",
		bytes.NewReader([]byte(`{"sha256":"`+digest+`"}`)))
	completeReq.AddCookie(cookie)
	completeRec := httptest.NewRecorder()
	mux.ServeHTTP(completeRec, completeReq)
	if completeRec.Code != http.StatusCreated {
		t.Fatalf("complete status = %d, want 201: %s", completeRec.Code, completeRec.Body.String())
	}
	ownReq := httptest.NewRequest(http.MethodGet, "/media/"+id, nil)
	ownReq.AddCookie(cookie)
	ownRec := httptest.NewRecorder()
	mux.ServeHTTP(ownRec, ownReq)
	if ownRec.Code != http.StatusOK {
		t.Fatalf("owner media status = %d, want 200: %s", ownRec.Code, ownRec.Body.String())
	}
	return ownRec.Body.Bytes()
}

// TestUploadOpenAcceptsPCMAndRefusesUnknown opens one upload as raw PCM
// through the real handler, stores one chunked take, and reads the exact
// bytes back. A type outside the closed set still completes to the refused
// type code with no blob stored.
func TestUploadOpenAcceptsPCMAndRefusesUnknown(t *testing.T) {
	mux, _ := openUploadMux(t)
	raw := sineRawPCM(8000, 1.0)
	id, cookie := openUpload(t, mux, "audio/pcm")
	if got := completeUpload(t, mux, id, cookie, raw); !bytes.Equal(got, raw) {
		t.Fatalf("stored %d bytes, want exactly the %d posted raw bytes", len(got), len(raw))
	}
	// The open accepts any named type. The allowlist refuses at
	// completion when the store persists, so the unknown take completes
	// to prove the refusal still lands there.
	stranger, strangerCookie := openUpload(t, mux, "audio/x-unknown")
	junk := []byte("bytes no allowlist entry covers")
	sum := sha256.Sum256(junk)
	digest := hex.EncodeToString(sum[:])
	putReq := httptest.NewRequest(http.MethodPut, api.UploadBasePath+"/"+stranger+"/chunks/0", bytes.NewReader(junk))
	putReq.Header.Set("X-Chunk-SHA256", digest)
	putReq.AddCookie(strangerCookie)
	putRec := httptest.NewRecorder()
	mux.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("unknown chunk status = %d, want 200: %s", putRec.Code, putRec.Body.String())
	}
	completeReq := httptest.NewRequest(http.MethodPost, api.UploadBasePath+"/"+stranger+"/complete",
		bytes.NewReader([]byte(`{"sha256":"`+digest+`"}`)))
	completeReq.AddCookie(strangerCookie)
	completeRec := httptest.NewRecorder()
	mux.ServeHTTP(completeRec, completeReq)
	if completeRec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("unknown complete status = %d, want 415: %s", completeRec.Code, completeRec.Body.String())
	}
	if !strings.Contains(completeRec.Body.String(), "unsupported_type") {
		t.Fatalf("unknown complete body = %s, want the refused type code", completeRec.Body.String())
	}
}

// TestWAVHeaderCarriesStoredRate builds one header from raw samples and
// pins every field, then proves empty, odd, and rateless builds refuse
// instead of writing a silent file.
func TestWAVHeaderCarriesStoredRate(t *testing.T) {
	const rate = 8000
	raw := sineRawPCM(rate, 1.0)
	if isWAVDocument(raw) {
		t.Fatal("raw PCM reads as headed, want headerless bytes")
	}
	if !isWAVDocument(sineWAV()) {
		t.Fatal("stored WAV reads as headerless, want the header recognized")
	}
	wav, err := wavFromMonoPCM(raw, rate)
	if err != nil {
		t.Fatalf("head raw PCM: %v", err)
	}
	if len(wav) != 44+len(raw) {
		t.Fatalf("headed length = %d, want %d", len(wav), 44+len(raw))
	}
	if string(wav[0:4]) != "RIFF" || string(wav[8:12]) != "WAVE" || string(wav[12:16]) != "fmt " || string(wav[36:40]) != "data" {
		t.Fatal("headed bytes carry no RIFF WAVE fmt data chunks")
	}
	if got := binary.LittleEndian.Uint16(wav[20:22]); got != 1 {
		t.Fatalf("format = %d, want PCM 1", got)
	}
	if got := binary.LittleEndian.Uint16(wav[22:24]); got != 1 {
		t.Fatalf("channels = %d, want mono", got)
	}
	if got := binary.LittleEndian.Uint32(wav[24:28]); got != rate {
		t.Fatalf("rate = %d, want %d", got, rate)
	}
	if got := binary.LittleEndian.Uint32(wav[28:32]); got != rate*2 {
		t.Fatalf("byte rate = %d, want %d", got, rate*2)
	}
	if got := binary.LittleEndian.Uint16(wav[32:34]); got != 2 {
		t.Fatalf("block align = %d, want 2", got)
	}
	if got := binary.LittleEndian.Uint16(wav[34:36]); got != 16 {
		t.Fatalf("bits = %d, want 16", got)
	}
	if got := binary.LittleEndian.Uint32(wav[40:44]); got != uint32(len(raw)) {
		t.Fatalf("data length = %d, want %d", got, len(raw))
	}
	if !bytes.Equal(wav[44:], raw) {
		t.Fatal("headed samples differ from the posted raw bytes")
	}
	if _, err := wavFromMonoPCM(nil, rate); !errors.Is(err, errRawStemEmpty) {
		t.Fatalf("empty build error = %v, want the empty stem refusal", err)
	}
	if _, err := wavFromMonoPCM([]byte{0x01}, rate); !errors.Is(err, errRawStemEmpty) {
		t.Fatalf("odd build error = %v, want the empty stem refusal", err)
	}
	if _, err := wavFromMonoPCM(raw, 0); !errors.Is(err, errStemRateInvalid) {
		t.Fatalf("rateless build error = %v, want the rate refusal", err)
	}
}

// TestPCMStemsCompleteLinksToDraft stores both stems as raw PCM, posts
// the completion, and requires the draft move with one scheduled pass.
// The stored files must carry headers after, and the probe must read the
// stored rate.
func TestPCMStemsCompleteLinksToDraft(t *testing.T) {
	fx := openWireFixture(t)
	sessionBroker := openWireBroker(t, fx)
	identitySvc, err := identity.New(t.Context(), identity.Config{DB: fx.db, SigningKey: "wire-test-signing-key"})
	if err != nil {
		t.Fatalf("open identity: %v", err)
	}
	session, cookie := mintWireSession(t, fx, sessionBroker)
	if cookie == nil {
		t.Fatal("mint set no guest cookie")
	}
	var owner string
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		`SELECT owner_id FROM sessions WHERE id = ?`, session.SessionID).Scan(&owner); err != nil {
		t.Fatalf("read session owner: %v", err)
	}
	ctx := t.Context()
	diary, err := broker.NewSQLiteDiary(fx.db)
	if err != nil {
		t.Fatalf("open diary: %v", err)
	}
	episodeID, _, err := diary.CreateEpisodeAndSession(ctx, owner, 1800)
	if err != nil {
		t.Fatalf("create episode: %v", err)
	}
	const rate = 8000
	raw := sineRawPCM(rate, 1.0)
	userID := persistRawStem(t, fx, owner, episodeID, raw)
	hostID := persistRawStem(t, fx, owner, episodeID, raw)
	episodeSvc, err := episode.NewService(episode.Config{DB: fx.db, TranscriptKind: kindEditTranscript})
	if err != nil {
		t.Fatalf("new episode service: %v", err)
	}
	drafts := openDraftJobs(t, fx)
	handler := identitySvc.Middleware(newStemsComplete(episodeSvc, drafts, fx.db))
	body, err := json.Marshal(stemsCompleteRequest{
		UserMediaID: userID, HostMediaID: hostID,
		UserSampleRate: rate, HostSampleRate: rate,
	})
	if err != nil {
		t.Fatalf("encode completion: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/episodes/"+episodeID+"/stems/complete", bytes.NewReader(body))
	req.AddCookie(cookie)
	req.SetPathValue("id", episodeID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("completion status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var answer stemsCompleteResponse
	if err := json.NewDecoder(rec.Body).Decode(&answer); err != nil {
		t.Fatalf("decode completion answer: %v", err)
	}
	if !answer.Moved || !answer.Scheduled || answer.JobID == "" {
		t.Fatalf("completion = %+v, want the draft move with one scheduled pass", answer)
	}
	if answer.State != string(episode.StateDraft) {
		t.Fatalf("completion state = %q, want draft", answer.State)
	}
	if got := transcriptJobsFor(t, drafts, episodeID); got != 1 {
		t.Fatalf("transcript passes = %d, want one scheduled pass", got)
	}
	for _, blobID := range []string{userID, hostID} {
		stored, err := os.ReadFile(filepath.Join(fx.pipe.mediaDir, blobID))
		if err != nil {
			t.Fatalf("read stored stem: %v", err)
		}
		if !isWAVDocument(stored) {
			t.Fatal("stored stem still reads headerless, want the WAV header")
		}
		length, err := ffmpeg.Duration(ctx, ffmpeg.Tools{}, filepath.Join(fx.pipe.mediaDir, blobID))
		if err != nil {
			t.Fatalf("probe stored stem: %v", err)
		}
		if math.Abs(length.Seconds()-1.0) > 0.001 {
			t.Fatalf("stored duration = %v, want 1s at the stored rate", length)
		}
	}
}

// TestStoredPCMDurationReadsStoredRate stores one raw take at a rate no
// other test uses and requires the duration to equal samples over that
// rate. The read path heads the bytes on the fly, so the probe never
// sees headerless audio even before any convert runs.
func TestStoredPCMDurationReadsStoredRate(t *testing.T) {
	fx := openWireFixture(t)
	const owner = "owner-pcm-duration"
	insertWireUser(t, fx, owner)
	ctx := t.Context()
	diary, err := broker.NewSQLiteDiary(fx.db)
	if err != nil {
		t.Fatalf("open diary: %v", err)
	}
	episodeID, _, err := diary.CreateEpisodeAndSession(ctx, owner, 1800)
	if err != nil {
		t.Fatalf("create episode: %v", err)
	}
	const rate = 16000
	const secs = 2.0
	raw := sineRawPCM(rate, secs)
	userID := persistRawStem(t, fx, owner, episodeID, raw)
	hostID := persistRawStem(t, fx, owner, episodeID, raw)
	linkStemRowsAtRate(t, fx, owner, episodeID, userID, hostID, rate)
	drafts := openDraftJobs(t, fx)
	userAudio, hostAudio, durationSecs, err := drafts.editInputs(ctx, owner, episodeID)
	if err != nil {
		t.Fatalf("read edit inputs: %v", err)
	}
	want := float64(len(raw)/2) / float64(rate)
	if math.Abs(durationSecs-want) > 0.001 {
		t.Fatalf("duration = %v, want %v from the stored rate", durationSecs, want)
	}
	if !isWAVDocument(userAudio) || !isWAVDocument(hostAudio) {
		t.Fatal("edit audio reads headerless, want headed bytes for the passes")
	}
	stored, err := os.ReadFile(filepath.Join(fx.pipe.mediaDir, userID))
	if err != nil {
		t.Fatalf("read stored stem: %v", err)
	}
	if !isWAVDocument(stored) {
		t.Fatal("stored stem still reads headerless, want the in place convert")
	}
	length, err := ffmpeg.Duration(ctx, ffmpeg.Tools{}, filepath.Join(fx.pipe.mediaDir, userID))
	if err != nil {
		t.Fatalf("probe stored stem: %v", err)
	}
	if math.Abs(length.Seconds()-want) > 0.001 {
		t.Fatalf("stored duration = %v, want %v from the stored rate", length, want)
	}
}
