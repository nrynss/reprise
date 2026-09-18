//go:build live

// Package gemini speaks to Gemini through Vertex AI over the generated
// client.
//
// This file holds the live access probe behind the live build tag. It never
// runs in CI. The probe proves the service account key resolves through the
// file source, sends a twenty minute Opus stem inline, and asks which of two
// deliveries should open the episode without showing the transcript. Audio
// is synthesized locally, so the task needs no committed clip and no real
// voice.
package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/auth/credentials"
	"github.com/nrynss/reprise/internal/settings"
	"google.golang.org/genai"
)

// probeModel names the editorial model from local settings.
const probeModel = "gemini-2.5-flash"

// resolveConfig points the loader at the checkout settings when the caller
// sets no explicit path. The test binary runs with its package directory
// as the working directory, so the loader fallback misses the checkout
// file. Walking up finds it without hardcoding any value from settings.
func resolveConfig(t *testing.T) {
	t.Helper()
	if v, ok := os.LookupEnv(settings.PathVar); ok && v != "" {
		t.Logf("config path from environment: %s", v)
		return
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for i := 0; i < 6; i++ {
		candidate := filepath.Join(dir, "config", "reprise.local.toml")
		if _, err := os.Stat(candidate); err == nil {
			t.Setenv(settings.PathVar, candidate)
			t.Logf("config path resolved: %s", candidate)
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("no checkout settings found and no config path in environment")
}

// writeProbeConfig writes a settings file that points the credential at
// the given path while keeping every other value loadable. The secrets
// the probe does not exercise resolve from a temp env file.
func writeProbeConfig(t *testing.T, keyPath string) string {
	t.Helper()
	dir := t.TempDir()
	env := filepath.Join(dir, "env")
	body := "ASSEMBLYAI_API_KEY=dummy-assembly-key-9f3k2\nSESSION_SIGNING_KEY=dummy-signing-key-4z8w1\n"
	if err := os.WriteFile(env, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	config := "[secrets.assemblyai_api_key]\nsource = \"env_file\"\npath = \"" + env + "\"\nvar = \"ASSEMBLYAI_API_KEY\"\n" +
		"[secrets.gemini_credential]\nsource = \"file\"\npath = \"" + keyPath + "\"\n" +
		"[secrets.session_signing_key]\nsource = \"env_file\"\npath = \"" + env + "\"\nvar = \"SESSION_SIGNING_KEY\"\n"
	path := filepath.Join(dir, "reprise.toml")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// proveBootRefusal shows a wrong or unreadable key file fails at boot and
// names the key, the source and the path. It uses temp configs only and
// makes no network call.
func proveBootRefusal(t *testing.T) {
	t.Helper()
	saved, hadEnv := os.LookupEnv(settings.PathVar)
	missing := filepath.Join(t.TempDir(), "no-such-key.json")
	t.Setenv(settings.PathVar, writeProbeConfig(t, missing))
	_, _, err := settings.Load(context.Background())
	if err == nil {
		t.Fatal("missing key file loaded without error")
	}
	text := err.Error()
	t.Logf("missing file error: %s", text)
	for _, want := range []string{"gemini_credential", "file", "no-such-key.json"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing file error names no %q: %s", want, text)
		}
	}
	loose := filepath.Join(t.TempDir(), "loose-key.json")
	if err := os.WriteFile(loose, []byte(`{"type":"service_account"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(loose, 0o640); err != nil {
		t.Fatal(err)
	}
	t.Setenv(settings.PathVar, writeProbeConfig(t, loose))
	looseLoaded, _, err := settings.Load(context.Background())
	if err == nil {
		_, err = looseLoaded.Secrets.GeminiCredential.Reveal()
	}
	if err == nil {
		t.Fatal("group readable key file revealed without error")
	}
	text = err.Error()
	for _, want := range []string{"gemini_credential", "file", "loose-key.json"} {
		if !strings.Contains(text, want) {
			t.Fatalf("loose file error names no %q: %s", want, text)
		}
	}
	if hadEnv {
		t.Setenv(settings.PathVar, saved)
	}
	os.Unsetenv(settings.PathVar)
	resolveConfig(t)
}

// newProbeClient loads settings, reveals the service account key JSON,
// and returns a Vertex client bound to the project and location from
// settings. It logs only lengths and names, never the key bytes.
func newProbeClient(t *testing.T) (*genai.Client, settings.Settings) {
	t.Helper()
	resolveConfig(t)
	loaded, plan, err := settings.Load(context.Background())
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	t.Logf("plan: %s", plan.String())
	if loaded.VertexProject == "" || loaded.VertexLocation == "" {
		t.Fatal("vertex project or location resolved empty")
	}
	t.Logf("project length %d location %s", len(loaded.VertexProject), loaded.VertexLocation)
	keyJSON, err := loaded.Secrets.GeminiCredential.Reveal()
	if err != nil {
		t.Fatalf("reveal gemini credential: %v", err)
	}
	if len(keyJSON) == 0 {
		t.Fatal("gemini credential resolved empty")
	}
	t.Logf("credential bytes %d", len(keyJSON))
	var shape struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(keyJSON), &shape); err != nil {
		t.Fatalf("credential is not JSON: %v", err)
	}
	if shape.Type != "service_account" {
		t.Fatalf("credential type %q, want service_account", shape.Type)
	}
	creds, err := credentials.NewCredentialsFromJSON(credentials.ServiceAccount, []byte(keyJSON), &credentials.DetectOptions{Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"}})
	if err != nil {
		t.Fatalf("build credentials: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Project:     loaded.VertexProject,
		Location:    loaded.VertexLocation,
		Backend:     genai.BackendVertexAI,
		Credentials: creds,
	})
	if err != nil {
		t.Fatalf("new vertex client: %v", err)
	}
	return client, loaded
}

// synthVoice renders one sentence to a WAV file with the local neural
// voice. Piper runs offline, so no network call and no key is involved.
func synthVoice(t *testing.T, voice, sentence, out string) {
	t.Helper()
	cmd := exec.Command("piper", "-m", voice, "-f", out)
	cmd.Stdin = strings.NewReader(sentence)
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("synthesize sentence: %v %.500s", err, raw)
	}
}

// concatWAV joins mono 16 bit WAV files of one rate into a single file.
func concatWAV(t *testing.T, parts []string, rate int, out string) {
	t.Helper()
	var pcm []int16
	for _, part := range parts {
		raw, err := os.ReadFile(part)
		if err != nil {
			t.Fatalf("read wav: %v", err)
		}
		if len(raw) < 44 || string(raw[0:4]) != "RIFF" {
			t.Fatalf("not a wav file: %s", part)
		}
		body := raw[44:]
		for i := 0; i+1 < len(body); i += 2 {
			pcm = append(pcm, int16(body[i])|int16(body[i+1])<<8)
		}
	}
	buf := make([]byte, 0, 44+len(pcm)*2)
	buf = append(buf, "RIFF"...)
	size := uint32(36 + len(pcm)*2)
	buf = append(buf, byte(size), byte(size>>8), byte(size>>16), byte(size>>24))
	buf = append(buf, "WAVEfmt "...)
	buf = append(buf, 16, 0, 0, 0, 1, 0, 1, 0)
	buf = append(buf, byte(rate), byte(rate>>8), byte(rate>>16), byte(rate>>24))
	bytex := uint32(rate * 2)
	buf = append(buf, byte(bytex), byte(bytex>>8), byte(bytex>>16), byte(bytex>>24))
	buf = append(buf, 2, 0, 16, 0, 'd', 'a', 't', 'a')
	dataSize := uint32(len(pcm) * 2)
	buf = append(buf, byte(dataSize), byte(dataSize>>8), byte(dataSize>>16), byte(dataSize>>24))
	for _, s := range pcm {
		buf = append(buf, byte(s), byte(uint16(s)>>8))
	}
	if err := os.WriteFile(out, buf, 0o600); err != nil {
		t.Fatalf("write wav: %v", err)
	}
}

// encodeOpus converts a WAV file to a mono Opus file with ffmpeg.
func encodeOpus(t *testing.T, wav, opus string) {
	t.Helper()
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-i", wav, "-map", "a", "-c:a", "libopus", "-b:a", "24k", "-ac", "1", opus)
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("encode opus: %v %.500s", err, raw)
	}
}

// logUsage records the usage fields one response reports.
func logUsage(t *testing.T, label string, meta *genai.GenerateContentResponseUsageMetadata) {
	t.Helper()
	if meta == nil {
		t.Logf("%s usage: none reported", label)
		return
	}
	t.Logf("%s usage: prompt %d candidates %d total %d cached %d thoughts %d",
		label, meta.PromptTokenCount, meta.CandidatesTokenCount,
		meta.TotalTokenCount, meta.CachedContentTokenCount, meta.ThoughtsTokenCount)
	for _, detail := range meta.PromptTokensDetails {
		t.Logf("%s prompt detail: modality %s count %d", label, detail.Modality, detail.TokenCount)
	}
	for _, detail := range meta.CandidatesTokensDetails {
		t.Logf("%s candidates detail: modality %s count %d", label, detail.Modality, detail.TokenCount)
	}
}

// generate sends one prompt with optional audio parts and returns the text.
func generate(t *testing.T, client *genai.Client, parts []*genai.Part, maxTokens int32) *genai.GenerateContentResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Second)
	defer cancel()
	resp, err := client.Models.GenerateContent(ctx, probeModel,
		[]*genai.Content{{Role: "user", Parts: parts}},
		&genai.GenerateContentConfig{MaxOutputTokens: maxTokens})
	if err != nil {
		t.Fatalf("generate content: %v", err)
	}
	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		t.Fatal("no candidates returned")
	}
	for i, c := range resp.Candidates {
		t.Logf("candidate %d finish %s parts %d", i, c.FinishReason, len(c.Content.Parts))
	}
	return resp
}

func TestGeminiProbe(t *testing.T) {
	proveBootRefusal(t)
	client, loaded := newProbeClient(t)
	dir := t.TempDir()
	voice := "/tmp/voices/en_US-lessac-medium.onnx"

	// Question one: credentials and a minimal call. The ping proves the
	// key reaches Vertex with the project and location from settings.
	ping := generate(t, client, []*genai.Part{
		genai.NewPartFromText("Reply with the single word PONG."),
	}, 64)
	t.Logf("ping text: %q model %s", ping.Text(), ping.ModelVersion)
	logUsage(t, "ping", ping.UsageMetadata)

	// Question two: audio size. Build a twenty minute stem locally by
	// looping a synthesized paragraph, then send it inline as Opus.
	sentences := []string{
		"The harbor ferry leaves at dawn and crosses the grey water slowly.",
		"Gulls follow the wake while the crew coils rope on the stern deck.",
		"A lighthouse blinks twice, then the foghorn answers from the point.",
		"Passengers sip coffee and watch the shoreline slide quietly past.",
	}
	var parts []string
	for i, s := range sentences {
		out := filepath.Join(dir, fmt.Sprintf("para-%d.wav", i))
		synthVoice(t, voice, s, out)
		parts = append(parts, out)
	}
	loop := filepath.Join(dir, "loop.wav")
	concatWAV(t, parts, 22050, loop)
	stem := filepath.Join(dir, "stem.wav")
	var stemParts []string
	for i := 0; i < 80; i++ {
		stemParts = append(stemParts, loop)
	}
	concatWAV(t, stemParts, 22050, stem)
	opus := filepath.Join(dir, "stem.opus")
	encodeOpus(t, stem, opus)
	raw, err := os.ReadFile(opus)
	if err != nil {
		t.Fatalf("read opus: %v", err)
	}
	info, err := os.Stat(opus)
	if err != nil {
		t.Fatalf("stat opus: %v", err)
	}
	probe := exec.Command("ffprobe", "-hide_banner", "-i", opus)
	probeOut, err := probe.CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe stem: %v %.500s", err, probeOut)
	}
	t.Logf("stem bytes %d probe %.500s", info.Size(), probeOut)
	heard := generate(t, client, []*genai.Part{
		genai.NewPartFromBytes(raw, "audio/ogg"),
		genai.NewPartFromText("Describe in one sentence what this audio sounds like."),
	}, 200)
	t.Logf("audio text: %.1000s model %s", heard.Text(), heard.ModelVersion)
	logUsage(t, "audio", heard.UsageMetadata)

	// Question three: listening, not reading. Two deliveries of one line,
	// one flat and one carrying laughter, judged without any transcript.
	flat := filepath.Join(dir, "flat.wav")
	laugh := filepath.Join(dir, "laugh.wav")
	synthVoice(t, voice, "And that is why the lighthouse keeper never trusts the fog.", flat)
	synthVoice(t, voice, "And that is why the lighthouse keeper never trusts the fog. Ha ha, he learned that the wet way.", laugh)
	flatOpus := filepath.Join(dir, "flat.opus")
	laughOpus := filepath.Join(dir, "laugh.opus")
	encodeOpus(t, flat, flatOpus)
	encodeOpus(t, laugh, laughOpus)
	flatRaw, err := os.ReadFile(flatOpus)
	if err != nil {
		t.Fatalf("read flat opus: %v", err)
	}
	laughRaw, err := os.ReadFile(laughOpus)
	if err != nil {
		t.Fatalf("read laugh opus: %v", err)
	}
	choice := generate(t, client, []*genai.Part{
		genai.NewPartFromText("Two short voice takes follow. Do not transcribe them. " +
			"Listen to the delivery only and say which take should open the episode, " +
			"the first or the second, with one reason grounded in how it sounds."),
		genai.NewPartFromBytes(flatRaw, "audio/ogg"),
		genai.NewPartFromBytes(laughRaw, "audio/ogg"),
	}, 4000)
	t.Logf("flat bytes %d laugh bytes %d", len(flatRaw), len(laughRaw))
	t.Logf("choice full text: %s", choice.Text())
	t.Logf("choice model %s", choice.ModelVersion)
	logUsage(t, "choice", choice.UsageMetadata)

	_ = loaded
}
