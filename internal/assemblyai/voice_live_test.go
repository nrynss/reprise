//go:build live

// Live probe for the browser setup frame. It sends the golden session
// update the web client transmits, with test text standing in for the
// product brief, and asserts the provider echoes that text back. It spends
// real money, so the token carries a two minute cap and the run deletes its
// session. It never runs in CI.
package assemblyai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// voiceSetupProbePrompt is the test brief the probe sends. It keeps replies
// short, so the run ends fast.
const voiceSetupProbePrompt = "You are a test host for the setup probe. Keep replies to one short sentence."

// voiceSetupProbeGreeting is the test greeting the probe sends. The final
// transcript must match it exactly.
const voiceSetupProbeGreeting = "Setup probe greeting. Do you hear the test brief?"

// voiceSetupProbeFrame loads the golden frame the web client sends and swaps
// the product brief for test text. The shape on the wire stays the golden
// one, so the probe measures what the browser transmits.
func voiceSetupProbeFrame(t *testing.T) map[string]any {
	t.Helper()
	path := filepath.Join("..", "..", "web", "src", "lib", "voice", "testdata", "session-update.golden.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden setup frame: %v", err)
	}
	var frame map[string]any
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatalf("decode golden setup frame: %v", err)
	}
	session, ok := frame["session"].(map[string]any)
	if !ok {
		t.Fatal("golden setup frame holds no session object")
	}
	session["system_prompt"] = voiceSetupProbePrompt
	session["greeting"] = voiceSetupProbeGreeting
	return frame
}

// TestVoiceSessionUpdateProbe sends the golden setup frame with test text
// and asserts the provider echoes both lines. It then ends the session and
// deletes the record.
func TestVoiceSessionUpdateProbe(t *testing.T) {
	ctx := context.Background()
	client := newVoiceLiveClient(t)
	start := time.Now()

	sock := client.connectVoiceLiveSocket(ctx, client.mintToken(ctx, voiceLiveTokenCapSeconds), start)
	sock.sendJSON(voiceSetupProbeFrame(t))
	updated := sock.waitFor("session.updated", voiceLiveWaitTimeout, func(e voiceLiveEvent) bool {
		return isVoiceLiveEvent(e, "session.updated")
	})
	config, _ := updated.Raw["config"].(map[string]any)
	if config == nil {
		t.Fatal("session.updated holds no config object")
	}
	if got, _ := config["system_prompt"].(string); got != voiceSetupProbePrompt {
		t.Fatalf("provider echoed prompt %q, want %q", got, voiceSetupProbePrompt)
	}
	if got, _ := config["greeting"].(string); got != voiceSetupProbeGreeting {
		t.Fatalf("provider echoed greeting %q, want %q", got, voiceSetupProbeGreeting)
	}
	ready := sock.waitFor("session.ready", voiceLiveWaitTimeout, func(e voiceLiveEvent) bool {
		return isVoiceLiveEvent(e, "session.ready")
	})
	sessionID, _ := ready.Raw["session_id"].(string)
	if sessionID == "" {
		t.Fatal("session.ready holds no session id")
	}
	sock.sendJSON(map[string]any{"type": "session.end"})
	ended := sock.waitFor("session.ended", voiceLiveWaitTimeout, func(e voiceLiveEvent) bool {
		return isVoiceLiveEvent(e, "session.ended")
	})
	sock.close()
	voiceLiveLog(t, sock.snapshot(), start)
	if duration, ok := ended.Raw["session_duration_seconds"].(float64); ok {
		t.Logf("setup probe session %s billed %.2fs", sessionID, duration)
	}

	status, body := client.sessionsCall(ctx, http.MethodDelete, "/v1/sessions/"+sessionID, nil)
	if status != http.StatusNoContent && status != http.StatusOK {
		t.Fatalf("delete session %s: status %d: %.200s", sessionID, status, body)
	}
	_, listBody := client.sessionsCall(ctx, http.MethodGet, "/v1/sessions", url.Values{"limit": {"200"}})
	if voiceLiveContains(voiceLiveSessionIDs(listBody), sessionID) {
		t.Fatalf("session %s still listed after delete", sessionID)
	}
	t.Logf("tokens minted: %d, wall time: %s", client.tokens, time.Since(start).Truncate(time.Second))
}
