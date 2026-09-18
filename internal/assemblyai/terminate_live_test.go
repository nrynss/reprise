//go:build live

// Live measurement for ending a session from the server side. It opens one
// real session, deletes it through the Sessions API while the socket stays
// open, and records what the socket sees and what the API reports after.
// One session of about a minute costs under ten cents. The token cap bounds
// a lost session at the same ceiling the other live probes keep.
package assemblyai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// TestTerminateLiveProbe deletes a connected session and records the raw
// answers: the delete status, every socket event after the delete, and the
// session record before and after. It ends the socket explicitly when the
// server leaves it open, so no run bills past the observation window.
func TestTerminateLiveProbe(t *testing.T) {
	ctx := context.Background()
	client := newVoiceLiveClient(t)
	start := time.Now()

	sock := client.connectVoiceLiveSocket(ctx, client.mintToken(ctx, voiceLiveTokenCapSeconds), start)
	sock.sendJSON(map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"system_prompt": "You are a test host. Keep replies to one short sentence.",
			"greeting":      "Hi. Terminate test.",
			"output":        map[string]any{"voice": "alba"},
		},
	})
	ready := sock.waitFor("session.ready", voiceLiveWaitTimeout, func(e voiceLiveEvent) bool {
		return isVoiceLiveEvent(e, "session.ready")
	})
	sessionID, _ := ready.Raw["session_id"].(string)
	if sessionID == "" {
		t.Fatal("session.ready holds no session id")
	}
	t.Logf("terminate session %s", sessionID)
	sock.waitFor("greeting reply.done", voiceLiveWaitTimeout, func(e voiceLiveEvent) bool {
		return isVoiceLiveEvent(e, "reply.done")
	})

	beforeStatus, beforeBody := client.sessionsCall(ctx, http.MethodGet, "/v1/sessions/"+sessionID, nil)
	t.Logf("before delete: status %d body %.2000s", beforeStatus, beforeBody)

	deleteStatus, deleteBody := client.sessionsCall(ctx, http.MethodDelete, "/v1/sessions/"+sessionID, nil)
	t.Logf("delete: status %d body %.2000s", deleteStatus, deleteBody)
	if deleteStatus != http.StatusNoContent {
		t.Fatalf("delete status %d, want 204", deleteStatus)
	}

	deleteAt := time.Now()
	observed := observeSocket(t, sock, 15*time.Second)
	for _, event := range observed {
		t.Logf("post-delete socket event +%s %s", event.At.Sub(deleteAt).Truncate(time.Second), voiceLiveRedacted(event.Raw))
	}
	for _, event := range observed {
		if isVoiceLiveEvent(event, "session.ended") || isVoiceLiveEvent(event, "session.error") {
			t.Fatalf("socket closed after delete: %s", voiceLiveRedacted(event.Raw))
		}
	}
	if len(observed) == 0 {
		t.Logf("post-delete socket: no new events in 15 seconds")
	}
	select {
	case <-sock.done:
		t.Fatalf("post-delete socket: server closed the wire after delete")
	default:
		t.Logf("post-delete socket: wire still open")
	}

	afterStatus, afterBody := client.sessionsCall(ctx, http.MethodGet, "/v1/sessions/"+sessionID, nil)
	t.Logf("after delete: status %d body %.2000s", afterStatus, afterBody)
	if afterStatus != http.StatusNotFound {
		t.Fatalf("fetch after delete status %d, want 404", afterStatus)
	}
	firstDuration := decodeDuration(t, afterBody)
	if firstDuration != -1 {
		t.Fatalf("duration after delete %.3f, want absent", firstDuration)
	}

	_, listBody := client.sessionsCall(ctx, http.MethodGet, "/v1/sessions", url.Values{"limit": {"200"}})
	t.Logf("after delete: session listed=%v", voiceLiveContains(voiceLiveSessionIDs(listBody), sessionID))
	if voiceLiveContains(voiceLiveSessionIDs(listBody), sessionID) {
		t.Fatalf("session %s still listed after delete", sessionID)
	}

	time.Sleep(20 * time.Second)
	laterStatus, laterBody := client.sessionsCall(ctx, http.MethodGet, "/v1/sessions/"+sessionID, nil)
	t.Logf("20 seconds later: status %d body %.2000s", laterStatus, laterBody)
	if laterStatus != http.StatusNotFound {
		t.Fatalf("fetch 20 seconds later status %d, want 404", laterStatus)
	}
	laterDuration := decodeDuration(t, laterBody)
	t.Logf("duration first %.3f later %.3f", firstDuration, laterDuration)

	select {
	case <-sock.done:
		t.Fatalf("cleanup: socket closed on its own after delete, the server may end sessions after all")
	default:
		sock.sendJSON(map[string]any{"type": "session.end"})
		ended := sock.waitFor("session.ended", voiceLiveWaitTimeout, func(e voiceLiveEvent) bool {
			return isVoiceLiveEvent(e, "session.ended")
		})
		t.Logf("cleanup session.ended: %s", voiceLiveRedacted(ended.Raw))
		// The test itself waited 15 seconds plus 20 seconds after the
		// delete. A duration past 30 proves billing ran through it.
		duration, _ := ended.Raw["session_duration_seconds"].(float64)
		if duration <= 30 {
			t.Fatalf("cleanup billed %.3fs, want past 30: billing stopped at the delete", duration)
		}
		if _, ok := ended.Raw["audio_duration_seconds"]; ok {
			if ended.Raw["audio_duration_seconds"] != nil {
				t.Fatalf("audio duration reads %v, want null", ended.Raw["audio_duration_seconds"])
			}
		}
	}
	sock.close()

	secondDeleteStatus, secondDeleteBody := client.sessionsCall(ctx, http.MethodDelete, "/v1/sessions/"+sessionID, nil)
	t.Logf("second delete: status %d body %.2000s", secondDeleteStatus, secondDeleteBody)
	if secondDeleteStatus != http.StatusNotFound {
		t.Fatalf("second delete status %d, want 404", secondDeleteStatus)
	}

	_, finalBody := client.sessionsCall(ctx, http.MethodGet, "/v1/sessions", url.Values{"limit": {"200"}})
	if voiceLiveContains(voiceLiveSessionIDs(finalBody), sessionID) {
		t.Fatalf("session %s still listed after delete", sessionID)
	}

	if float64(client.tokens)*voiceLiveTokenCapSeconds/3600*voiceLivePricePerHourCents > 100 {
		t.Fatalf("spend cap exceeded")
	}
	t.Logf("tokens minted: %d, wall time: %s", client.tokens, time.Since(start).Truncate(time.Second))
}

// observeSocket returns the events that arrive in the window after the call.
func observeSocket(t *testing.T, sock *voiceLiveSocket, window time.Duration) []voiceLiveEvent {
	t.Helper()
	seen := len(sock.snapshot())
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		select {
		case <-sock.done:
			return sock.snapshot()[seen:]
		case <-time.After(voiceLivePollInterval):
		}
	}
	return sock.snapshot()[seen:]
}

// decodeDuration reads duration_seconds from a session record, or -1 when
// the field is absent or null, which is how open sessions report.
func decodeDuration(t *testing.T, body []byte) float64 {
	t.Helper()
	var decoded struct {
		Duration *float64 `json:"duration_seconds"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode session record: %v", err)
	}
	if decoded.Duration == nil {
		return -1
	}
	return *decoded.Duration
}
