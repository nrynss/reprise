//go:build live

// Live probe for the Voice Agent API. It mints short-lived tokens,
// streams generated speech into real sessions, and records every event.
// Generated clips live beside the test. No real voice enters the probe.
package assemblyai

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/nrynss/reprise/internal/settings"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

const voiceLiveHost = "agents.assemblyai.com"

const voiceLivePricePerHourCents = 450

const voiceLiveChunkSamples = 4800

const voiceLiveStreamInterval = 200 * time.Millisecond

const voiceLiveSettleWait = 1500 * time.Millisecond

const voiceLiveWaitTimeout = 60 * time.Second

const voiceLivePollInterval = 200 * time.Millisecond

const voiceLiveSessionTimeout = 90 * time.Second

const voiceLiveTokenCapSeconds = 120

const voiceLiveCapProbeSeconds = 60

const voiceLiveGreeting = "Hello there. Last week you mentioned dreading a talk with your sister. Did it happen?"

// voiceLiveEvent is one decoded socket frame with its local clock time.
type voiceLiveEvent struct {
	At   time.Time
	Type string
	Raw  map[string]any
}

// voiceLiveText returns the text field of an event, or empty when absent.
func voiceLiveText(event voiceLiveEvent) string {
	text, _ := event.Raw["text"].(string)
	return text
}

// voiceLiveClient speaks to the Sessions API and mints tokens. The key comes
// from the settings loader, so the value never passes through the test.
type voiceLiveClient struct {
	t      *testing.T
	key    string
	http   *http.Client
	tokens int
}

// newVoiceLiveClient loads settings and reveals the provider key for the run.
func newVoiceLiveClient(t *testing.T) *voiceLiveClient {
	t.Helper()
	loaded, _, err := settings.Load(context.Background())
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	key, err := loaded.Secrets.AssemblyAIAPIKey.Reveal()
	if err != nil {
		t.Fatalf("reveal provider key: %v", err)
	}
	if key == "" {
		t.Fatal("provider key is empty")
	}
	return &voiceLiveClient{t: t, key: key, http: &http.Client{Timeout: 60 * time.Second}}
}

// mintToken mints one single-use token with the given session cap in seconds.
func (c *voiceLiveClient) mintToken(ctx context.Context, maxSessionSeconds int) string {
	c.t.Helper()
	query := url.Values{}
	query.Set("expires_in_seconds", "300")
	query.Set("max_session_duration_seconds", fmt.Sprint(maxSessionSeconds))
	endpoint := "https://" + voiceLiveHost + "/v1/token?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		c.t.Fatalf("build token request: %v", err)
	}
	req.Header.Set("Authorization", c.key)
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("mint token: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatalf("read token response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		c.t.Fatalf("mint token: status %d: %.200s", resp.StatusCode, body)
	}
	var decoded struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		c.t.Fatalf("decode token response: %v", err)
	}
	if decoded.Token == "" {
		c.t.Fatal("token response holds no token")
	}
	c.tokens++
	return decoded.Token
}

// sessionsCall runs one Sessions API request and returns status and body.
func (c *voiceLiveClient) sessionsCall(ctx context.Context, method string, path string, query url.Values) (int, []byte) {
	c.t.Helper()
	endpoint := "https://" + voiceLiveHost + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		c.t.Fatalf("build sessions request: %v", err)
	}
	req.Header.Set("Authorization", c.key)
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("sessions %s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatalf("read sessions response: %v", err)
	}
	return resp.StatusCode, body
}

// voiceLiveSocket is one open voice session with a background reader.
type voiceLiveSocket struct {
	t       *testing.T
	conn    net.Conn
	mu      sync.Mutex
	events  []voiceLiveEvent
	done    chan struct{}
	started time.Time
}

// voiceLiveMaskedFrame encodes one masked client text frame with the length
// form the size needs: short, 16 bit, or 64 bit.
func voiceLiveMaskedFrame(payload []byte) []byte {
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		panic(err)
	}
	masked := make([]byte, len(payload))
	for i := range payload {
		masked[i] = payload[i] ^ mask[i%4]
	}
	var frame []byte
	switch {
	case len(payload) < 126:
		frame = []byte{0x81, 0x80 | byte(len(payload))}
	case len(payload) < 65536:
		frame = []byte{0x81, 0x80 | 126, byte(len(payload) >> 8), byte(len(payload))}
	default:
		frame = []byte{0x81, 0x80 | 127, 0, 0, 0, 0,
			byte(len(payload) >> 24), byte(len(payload) >> 16), byte(len(payload) >> 8), byte(len(payload))}
	}
	return append(append(frame, mask[:]...), masked...)
}

// voiceLiveReadFrame reads one server frame and returns its opcode and body.
func voiceLiveReadFrame(conn net.Conn) (byte, []byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return 0, nil, err
	}
	opcode := header[0] & 0x0f
	length := int(header[1] & 0x7f)
	if length == 126 {
		ext := make([]byte, 2)
		if _, err := io.ReadFull(conn, ext); err != nil {
			return 0, nil, err
		}
		length = int(binary.BigEndian.Uint16(ext))
	} else if length == 127 {
		ext := make([]byte, 8)
		if _, err := io.ReadFull(conn, ext); err != nil {
			return 0, nil, err
		}
		length = int(binary.BigEndian.Uint64(ext))
	}
	masked := header[1]&0x80 != 0
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(conn, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return opcode, payload, nil
}

// voiceLiveReadHTTPStatus reads the handshake status line and headers.
func voiceLiveReadHTTPStatus(conn net.Conn) string {
	var head bytes.Buffer
	chunk := make([]byte, 1)
	for !strings.Contains(head.String(), "\r\n\r\n") {
		if _, err := conn.Read(chunk); err != nil {
			return "read error: " + err.Error()
		}
		head.Write(chunk)
		if head.Len() > 8192 {
			return "headers too long"
		}
	}
	line, _, _ := strings.Cut(head.String(), "\r\n")
	return line
}

// connectVoiceLiveSocket opens the voice socket with a token and starts reading.
func (c *voiceLiveClient) connectVoiceLiveSocket(ctx context.Context, token string, start time.Time) *voiceLiveSocket {
	c.t.Helper()
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	raw, err := dialer.DialContext(ctx, "tcp", voiceLiveHost+":443")
	if err != nil {
		c.t.Fatalf("dial voice host: %v", err)
	}
	conn := tls.Client(raw, &tls.Config{ServerName: voiceLiveHost})
	if err := conn.HandshakeContext(ctx); err != nil {
		c.t.Fatalf("tls handshake: %v", err)
	}
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		c.t.Fatalf("random websocket key: %v", err)
	}
	request := "GET /v1/ws?token=" + url.QueryEscape(token) + " HTTP/1.1\r\n" +
		"Host: " + voiceLiveHost + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + base64.StdEncoding.EncodeToString(keyBytes) + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(request)); err != nil {
		c.t.Fatalf("write websocket handshake: %v", err)
	}
	status := voiceLiveReadHTTPStatus(conn)
	if !strings.Contains(status, "101") {
		c.t.Fatalf("websocket handshake: %s", status)
	}
	_ = conn.SetDeadline(time.Time{})
	sock := &voiceLiveSocket{t: c.t, conn: conn, done: make(chan struct{}), started: start}
	go sock.readLoop()
	return sock
}

// readLoop decodes text frames into events until the socket closes.
func (s *voiceLiveSocket) readLoop() {
	defer close(s.done)
	var parts [][]byte
	flush := func() {
		if len(parts) == 0 {
			return
		}
		raw := bytes.Join(parts, nil)
		parts = nil
		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return
		}
		eventType, _ := decoded["type"].(string)
		s.mu.Lock()
		s.events = append(s.events, voiceLiveEvent{At: time.Now(), Type: eventType, Raw: decoded})
		s.mu.Unlock()
	}
	for {
		opcode, payload, err := voiceLiveReadFrame(s.conn)
		if err != nil {
			return
		}
		switch opcode {
		case 0x0:
			parts = append(parts, payload)
		case 0x1:
			parts = append(parts, payload)
			flush()
		case 0x2:
			var decoded map[string]any
			if err := json.Unmarshal(payload, &decoded); err == nil {
				eventType, _ := decoded["type"].(string)
				s.mu.Lock()
				s.events = append(s.events, voiceLiveEvent{At: time.Now(), Type: eventType, Raw: decoded})
				s.mu.Unlock()
			}
		case 0x8:
			_ = s.conn.Close()
			return
		case 0x9:
			frame := []byte{0x8a, byte(len(payload))}
			_ = s.writeRaw(append(frame, payload...))
		}
	}
}

// writeRaw writes one frame under the socket lock. It reports a closed
// socket instead of failing the test, because the server may close first.
func (s *voiceLiveSocket) writeRaw(frame []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	_, err := s.conn.Write(frame)
	_ = s.conn.SetWriteDeadline(time.Time{})
	return err
}

// sendJSON sends one JSON message to the server.
func (s *voiceLiveSocket) sendJSON(obj any) {
	s.t.Helper()
	payload, err := json.Marshal(obj)
	if err != nil {
		s.t.Fatalf("encode socket message: %v", err)
	}
	if err := s.writeRaw(voiceLiveMaskedFrame(payload)); err != nil {
		s.t.Fatalf("send socket message: %v", err)
	}
}

// snapshot copies the events recorded so far.
func (s *voiceLiveSocket) snapshot() []voiceLiveEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]voiceLiveEvent(nil), s.events...)
}

// count returns how many events of one type arrived so far.
func (s *voiceLiveSocket) count(eventType string) int {
	total := 0
	for _, event := range s.snapshot() {
		if event.Type == eventType {
			total++
		}
	}
	return total
}

// waitFor blocks until an event matches or the socket closes first.
func (s *voiceLiveSocket) waitFor(what string, timeout time.Duration, match func(voiceLiveEvent) bool) voiceLiveEvent {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, event := range s.snapshot() {
			if match(event) {
				return event
			}
		}
		select {
		case <-s.done:
			for _, event := range s.snapshot() {
				if match(event) {
					return event
				}
			}
			s.t.Fatalf("socket closed before %s", what)
		case <-time.After(voiceLivePollInterval):
		}
	}
	s.t.Fatalf("timed out waiting for %s", what)
	return voiceLiveEvent{}
}

// close waits for the reader to stop and releases the socket. The server
// closes the wire on session.ended, so the test never writes a close frame
// of its own. Closing twice is safe.
func (s *voiceLiveSocket) close() {
	_ = s.conn.Close()
	select {
	case <-s.done:
	case <-time.After(10 * time.Second):
	}
}

// voiceLiveLoadClip reads one committed PCM clip from testdata.
func voiceLiveLoadClip(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/speech/" + name)
	if err != nil {
		t.Fatalf("read clip %s: %v", name, err)
	}
	return raw
}

// voiceLiveStreamClip streams PCM at realtime rate, then waits for uptake.
func voiceLiveStreamClip(t *testing.T, sock *voiceLiveSocket, pcm []byte) {
	t.Helper()
	for offset := 0; offset < len(pcm); offset += voiceLiveChunkSamples * 2 {
		end := offset + voiceLiveChunkSamples*2
		if end > len(pcm) {
			end = len(pcm)
		}
		sock.sendJSON(map[string]any{
			"type":  "input.audio",
			"audio": base64.StdEncoding.EncodeToString(pcm[offset:end]),
		})
		time.Sleep(voiceLiveStreamInterval)
	}
	time.Sleep(voiceLiveSettleWait)
}

// voiceLiveFind returns every event of one type.
func voiceLiveFind(events []voiceLiveEvent, eventType string) []voiceLiveEvent {
	var found []voiceLiveEvent
	for _, event := range events {
		if event.Type == eventType {
			found = append(found, event)
		}
	}
	return found
}

// voiceLiveLastText returns the text of the last event of one type.
func voiceLiveLastText(events []voiceLiveEvent, eventType string) string {
	found := voiceLiveFind(events, eventType)
	if len(found) == 0 {
		return ""
	}
	return voiceLiveText(found[len(found)-1])
}

// voiceLiveRedacted renders an event with audio and token bytes hidden.
func voiceLiveRedacted(raw map[string]any) string {
	redacted := make(map[string]any, len(raw))
	for key, value := range raw {
		switch key {
		case "audio", "data", "token", "resume_token":
			redacted[key] = "<redacted>"
		default:
			redacted[key] = value
		}
	}
	encoded, err := json.Marshal(redacted)
	if err != nil {
		return fmt.Sprintf("%v", raw)
	}
	return string(encoded)
}

// voiceLiveLog records every non-audio event with its local clock offset.
func voiceLiveLog(t *testing.T, events []voiceLiveEvent, start time.Time) {
	t.Helper()
	for _, event := range events {
		if event.Type == "reply.audio" {
			continue
		}
		elapsed := event.At.Sub(start).Truncate(time.Millisecond)
		t.Logf("+%.3fs %s %s", elapsed.Seconds(), event.Type, voiceLiveRedacted(event.Raw))
	}
}

// voiceLiveSessionIDs extracts session ids from a list response.
func voiceLiveSessionIDs(body []byte) []string {
	var decoded struct {
		Sessions []struct {
			ID string `json:"id"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil
	}
	ids := make([]string, 0, len(decoded.Sessions))
	for _, session := range decoded.Sessions {
		ids = append(ids, session.ID)
	}
	return ids
}

// voiceLiveContains reports whether ids holds want.
func voiceLiveContains(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// voiceLiveDuration polls the Sessions API until the session completes.
func voiceLiveDuration(t *testing.T, client *voiceLiveClient, ctx context.Context, sessionID string) (float64, string) {
	t.Helper()
	deadline := time.Now().Add(voiceLiveSessionTimeout)
	for time.Now().Before(deadline) {
		status, body := client.sessionsCall(ctx, http.MethodGet, "/v1/sessions/"+sessionID, nil)
		if status != http.StatusOK {
			t.Fatalf("fetch session %s: status %d: %.200s", sessionID, status, body)
		}
		var decoded struct {
			Status   string  `json:"status"`
			Duration float64 `json:"duration_seconds"`
		}
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatalf("decode session %s: %v", sessionID, err)
		}
		if decoded.Status == "completed" && decoded.Duration > 0 {
			return decoded.Duration, string(body)
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("session %s never completed", sessionID)
	return 0, ""
}

func isVoiceLiveEvent(event voiceLiveEvent, eventType string) bool {
	return event.Type == eventType
}

// voiceLiveFFProbe runs ffprobe on the downloaded recording and logs the
// stream shape. The binary must exist where the test runs.
func voiceLiveFFProbe(t *testing.T, artifactDir string) {
	t.Helper()
	out, err := voiceLiveRun(t, "ffprobe", "-hide_banner", "-show_streams", "-show_format", artifactDir+"/audio.bin")
	if err != nil {
		t.Fatalf("ffprobe recording: %v: %s", err, out)
	}
	t.Logf("ffprobe recording:\n%s", out)
	if !strings.Contains(out, "codec_name=opus") {
		t.Fatalf("recording is not Opus: %s", out)
	}
	if !strings.Contains(out, "channels=2") {
		t.Fatalf("recording is not stereo: %s", out)
	}
}

// voiceLiveRun runs one binary and returns its combined output.
func voiceLiveRun(t *testing.T, name string, args ...string) (string, error) {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

// TestVoiceLiveProbe streams generated speech through real sessions and
// records every measurement the probe record needs. It spends real money,
// so every token carries a two minute cap and the run deletes its sessions.
func TestVoiceLiveProbe(t *testing.T) {
	ctx := context.Background()
	client := newVoiceLiveClient(t)
	start := time.Now()
	connectedSeconds := 0.0

	exchangePCM := voiceLiveLoadClip(t, "exchange-24k.pcm")
	keytermPCM := voiceLiveLoadClip(t, "keyterm-24k.pcm")

	greetSock := client.connectVoiceLiveSocket(ctx, client.mintToken(ctx, voiceLiveTokenCapSeconds), start)
	greetSock.sendJSON(map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"system_prompt": "You are a test host. Keep replies to one short sentence.",
			"greeting":      voiceLiveGreeting,
			"output":        map[string]any{"voice": "alba"},
		},
	})
	ready := greetSock.waitFor("session.ready", voiceLiveWaitTimeout, func(e voiceLiveEvent) bool {
		return isVoiceLiveEvent(e, "session.ready")
	})
	greetingSession, _ := ready.Raw["session_id"].(string)
	if greetingSession == "" {
		t.Fatal("session.ready holds no session id")
	}
	greetSock.waitFor("greeting transcript", voiceLiveWaitTimeout, func(e voiceLiveEvent) bool {
		return isVoiceLiveEvent(e, "transcript.agent")
	})
	greetSock.sendJSON(map[string]any{"type": "session.end"})
	ended := greetSock.waitFor("session.ended", voiceLiveWaitTimeout, func(e voiceLiveEvent) bool {
		return isVoiceLiveEvent(e, "session.ended")
	})
	greetSock.close()
	greetingEvents := greetSock.snapshot()
	voiceLiveLog(t, greetingEvents, start)
	if got := voiceLiveLastText(greetingEvents, "transcript.agent"); got != voiceLiveGreeting {
		t.Fatalf("greeting spoken %q, want %q", got, voiceLiveGreeting)
	}
	if duration, ok := ended.Raw["session_duration_seconds"].(float64); ok {
		connectedSeconds += duration
	}

	clipSock := client.connectVoiceLiveSocket(ctx, client.mintToken(ctx, voiceLiveTokenCapSeconds), start)
	clipSock.sendJSON(map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"system_prompt": "You are a test host. Reply in one short sentence. Do not ask follow-ups.",
			"greeting":      "Hi. Say nothing more until the guest speaks.",
			"output":        map[string]any{"voice": "alba"},
		},
	})
	clipReady := clipSock.waitFor("session.ready", voiceLiveWaitTimeout, func(e voiceLiveEvent) bool {
		return isVoiceLiveEvent(e, "session.ready")
	})
	clipSession, _ := clipReady.Raw["session_id"].(string)
	if clipSession == "" {
		t.Fatal("session.ready holds no session id")
	}
	clipSock.waitFor("greeting reply.done", voiceLiveWaitTimeout, func(e voiceLiveEvent) bool {
		return isVoiceLiveEvent(e, "reply.done")
	})
	voiceLiveStreamClip(t, clipSock, exchangePCM)
	clipSock.waitFor("transcript.user", voiceLiveWaitTimeout, func(e voiceLiveEvent) bool {
		return isVoiceLiveEvent(e, "transcript.user")
	})
	clipSock.waitFor("agent reply.done", voiceLiveWaitTimeout, func(e voiceLiveEvent) bool {
		return isVoiceLiveEvent(e, "reply.done") && clipSock.count("reply.done") >= 2
	})
	clipSock.sendJSON(map[string]any{"type": "session.end"})
	clipEnded := clipSock.waitFor("session.ended", voiceLiveWaitTimeout, func(e voiceLiveEvent) bool {
		return isVoiceLiveEvent(e, "session.ended")
	})
	clipSock.close()
	clipEvents := clipSock.snapshot()
	voiceLiveLog(t, clipEvents, start)
	if duration, ok := clipEnded.Raw["session_duration_seconds"].(float64); ok {
		connectedSeconds += duration
	}
	for _, event := range voiceLiveFind(clipEvents, "transcript.user") {
		for _, key := range []string{"words", "start_ms", "end_ms", "start", "end"} {
			if _, ok := event.Raw[key]; ok {
				t.Fatalf("transcript.user carries timings: %s", voiceLiveRedacted(event.Raw))
			}
		}
		if _, ok := event.Raw["text"]; !ok {
			t.Fatalf("transcript.user misses text: %s", voiceLiveRedacted(event.Raw))
		}
	}
	deltas := voiceLiveFind(clipEvents, "transcript.agent.delta")
	if len(deltas) == 0 {
		t.Fatal("no transcript.agent.delta events arrived")
	}
	for _, event := range deltas {
		if _, ok := event.Raw["start_ms"].(float64); !ok {
			t.Fatalf("delta misses start_ms: %s", voiceLiveRedacted(event.Raw))
		}
		if _, ok := event.Raw["end_ms"].(float64); !ok {
			t.Fatalf("delta misses end_ms: %s", voiceLiveRedacted(event.Raw))
		}
	}

	plainSock := client.connectVoiceLiveSocket(ctx, client.mintToken(ctx, voiceLiveTokenCapSeconds), start)
	plainSock.sendJSON(map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"system_prompt": "You are a test host. Reply in one short sentence.",
			"greeting":      "Hi. Please tell me about your friend.",
			"output":        map[string]any{"voice": "alba"},
		},
	})
	plainReady := plainSock.waitFor("session.ready", voiceLiveWaitTimeout, func(e voiceLiveEvent) bool {
		return isVoiceLiveEvent(e, "session.ready")
	})
	plainSession, _ := plainReady.Raw["session_id"].(string)
	if plainSession == "" {
		t.Fatal("session.ready holds no session id")
	}
	plainSock.waitFor("greeting reply.done", voiceLiveWaitTimeout, func(e voiceLiveEvent) bool {
		return isVoiceLiveEvent(e, "reply.done")
	})
	voiceLiveStreamClip(t, plainSock, keytermPCM)
	plainSock.waitFor("transcript.user", voiceLiveWaitTimeout, func(e voiceLiveEvent) bool {
		return isVoiceLiveEvent(e, "transcript.user")
	})
	plainSock.sendJSON(map[string]any{"type": "session.end"})
	plainEnded := plainSock.waitFor("session.ended", voiceLiveWaitTimeout, func(e voiceLiveEvent) bool {
		return isVoiceLiveEvent(e, "session.ended")
	})
	plainSock.close()
	plainEvents := plainSock.snapshot()
	plainText := voiceLiveLastText(plainEvents, "transcript.user")
	if duration, ok := plainEnded.Raw["session_duration_seconds"].(float64); ok {
		connectedSeconds += duration
	}

	keyedSock := client.connectVoiceLiveSocket(ctx, client.mintToken(ctx, voiceLiveTokenCapSeconds), start)
	keyedSock.sendJSON(map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"system_prompt": "You are a test host. Reply in one short sentence.",
			"greeting":      "Hi. Please tell me about your friend.",
			"input":         map[string]any{"keyterms": []string{"Zaffranil", "vistrex", "malquenar"}},
			"output":        map[string]any{"voice": "alba"},
		},
	})
	keyedReady := keyedSock.waitFor("session.ready", voiceLiveWaitTimeout, func(e voiceLiveEvent) bool {
		return isVoiceLiveEvent(e, "session.ready")
	})
	keyedSession, _ := keyedReady.Raw["session_id"].(string)
	if keyedSession == "" {
		t.Fatal("session.ready holds no session id")
	}
	keyedSock.waitFor("greeting reply.done", voiceLiveWaitTimeout, func(e voiceLiveEvent) bool {
		return isVoiceLiveEvent(e, "reply.done")
	})
	voiceLiveStreamClip(t, keyedSock, keytermPCM)
	keyedSock.waitFor("transcript.user", voiceLiveWaitTimeout, func(e voiceLiveEvent) bool {
		return isVoiceLiveEvent(e, "transcript.user")
	})
	keyedSock.sendJSON(map[string]any{"type": "session.end"})
	keyedEnded := keyedSock.waitFor("session.ended", voiceLiveWaitTimeout, func(e voiceLiveEvent) bool {
		return isVoiceLiveEvent(e, "session.ended")
	})
	keyedSock.close()
	keyedEvents := keyedSock.snapshot()
	keyedText := voiceLiveLastText(keyedEvents, "transcript.user")
	if duration, ok := keyedEnded.Raw["session_duration_seconds"].(float64); ok {
		connectedSeconds += duration
	}
	if plainText == keyedText {
		t.Fatalf("keyterms changed nothing: both read %q", plainText)
	}
	if !strings.Contains(strings.ToLower(keyedText), "zaffranil") {
		t.Fatalf("keyed transcript misses the invented name: %q", keyedText)
	}

	capSock := client.connectVoiceLiveSocket(ctx, client.mintToken(ctx, voiceLiveCapProbeSeconds), start)
	capSock.sendJSON(map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"system_prompt": "Say the greeting then stay silent.",
			"greeting":      "Hi. Cap test.",
			"output":        map[string]any{"voice": "alba"},
		},
	})
	capReady := capSock.waitFor("session.ready", voiceLiveWaitTimeout, func(e voiceLiveEvent) bool {
		return isVoiceLiveEvent(e, "session.ready")
	})
	capSession, _ := capReady.Raw["session_id"].(string)
	if capSession == "" {
		t.Fatal("session.ready holds no session id")
	}
	capSock.waitFor("greeting reply.done", voiceLiveWaitTimeout, func(e voiceLiveEvent) bool {
		return isVoiceLiveEvent(e, "reply.done")
	})
	time.Sleep(70 * time.Second)
	for _, event := range capSock.snapshot() {
		if isVoiceLiveEvent(event, "session.ended") || isVoiceLiveEvent(event, "session.error") {
			t.Fatalf("cap session closed inside its window: %s", voiceLiveRedacted(event.Raw))
		}
	}
	capSock.sendJSON(map[string]any{"type": "session.end"})
	capEnded := capSock.waitFor("session.ended", voiceLiveWaitTimeout, func(e voiceLiveEvent) bool {
		return isVoiceLiveEvent(e, "session.ended")
	})
	capSock.close()
	capEvents := capSock.snapshot()
	voiceLiveLog(t, capEvents, start)
	if duration, ok := capEnded.Raw["session_duration_seconds"].(float64); ok {
		connectedSeconds += duration
		t.Logf("cap session %s billed %.2fs after client end past the window", capSession, duration)
	}
	time.Sleep(5 * time.Second)
	status, body := client.sessionsCall(ctx, http.MethodGet, "/v1/sessions/"+capSession, nil)
	if status != http.StatusOK {
		t.Fatalf("fetch cap session: status %d: %.200s", status, body)
	}
	t.Logf("cap session record: %.600s", body)

	billed, record := voiceLiveDuration(t, client, ctx, clipSession)
	t.Logf("clip session %s billed %.2fs", clipSession, billed)
	t.Logf("clip session record: %.600s", record)

	artStatus, artBody := client.sessionsCall(ctx, http.MethodGet, "/v1/sessions/"+clipSession, nil)
	if artStatus != http.StatusOK {
		t.Fatalf("fetch artifacts session: status %d", artStatus)
	}
	var artDecoded struct {
		Artifacts []struct {
			Type        string `json:"type"`
			URL         string `json:"url"`
			ContentType string `json:"content_type"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(artBody, &artDecoded); err != nil {
		t.Fatalf("decode artifacts: %v", err)
	}
	if len(artDecoded.Artifacts) == 0 {
		t.Fatalf("session %s holds no artifacts", clipSession)
	}
	artifactDir := t.TempDir()
	for _, artifact := range artDecoded.Artifacts {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
		if err != nil {
			t.Fatalf("build artifact request: %v", err)
		}
		resp, err := client.http.Do(req)
		if err != nil {
			t.Fatalf("fetch %s artifact: %v", artifact.Type, err)
		}
		content, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("read %s artifact: %v", artifact.Type, err)
		}
		if len(content) == 0 {
			t.Fatalf("%s artifact is empty", artifact.Type)
		}
		if err := os.WriteFile(artifactDir+"/"+artifact.Type+".bin", content, 0o600); err != nil {
			t.Fatalf("store %s artifact: %v", artifact.Type, err)
		}
		t.Logf("artifact %s %s %d bytes", artifact.Type, artifact.ContentType, len(content))
	}
	voiceLiveFFProbe(t, artifactDir)

	_, listBody := client.sessionsCall(ctx, http.MethodGet, "/v1/sessions", url.Values{"limit": {"200"}})
	beforeIDs := voiceLiveSessionIDs(listBody)
	for _, id := range []string{greetingSession, clipSession, plainSession, keyedSession, capSession} {
		if !voiceLiveContains(beforeIDs, id) {
			t.Fatalf("session %s missing from list before delete", id)
		}
	}
	for _, id := range []string{greetingSession, clipSession, plainSession, keyedSession, capSession} {
		status, body := client.sessionsCall(ctx, http.MethodDelete, "/v1/sessions/"+id, nil)
		if status != http.StatusNoContent && status != http.StatusOK {
			t.Fatalf("delete session %s: status %d: %.200s", id, status, body)
		}
	}
	_, afterBody := client.sessionsCall(ctx, http.MethodGet, "/v1/sessions", url.Values{"limit": {"200"}})
	afterIDs := voiceLiveSessionIDs(afterBody)
	for _, id := range []string{greetingSession, clipSession, plainSession, keyedSession, capSession} {
		if voiceLiveContains(afterIDs, id) {
			t.Fatalf("session %s still listed after delete", id)
		}
	}

	elapsed := time.Since(start)
	t.Logf("tokens minted: %d, connected seconds: %.1f, wall time: %s", client.tokens, connectedSeconds, elapsed.Truncate(time.Second))
	if float64(client.tokens)*voiceLiveTokenCapSeconds/3600*voiceLivePricePerHourCents > 100 {
		t.Fatalf("spend cap exceeded")
	}
}
