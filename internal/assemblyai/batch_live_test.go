//go:build live

// Package assemblyai speaks to the AssemblyAI batch API over plain HTTP.
//
// This file holds the live batch probe behind the live build tag. It never
// runs in CI. The probe builds a 48 kHz stem with a local synthesizer,
// uploads it, transcribes it with the flagship model, and records every
// response shape. The clip is rebuilt on each run, so the task needs no
// committed audio and no fixture path.
package assemblyai

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/nrynss/reprise/internal/settings"
)

const batchProbeBaseURL = "https://api.assemblyai.com"
const batchProbeGatewayURL = "https://llm-gateway.assemblyai.com/v1/chat/completions"

// probeSentences is the generated script. Each sentence is synthesized alone
// and joined with a fixed silence, so every sentence boundary is known
// before the upload. Word bounds come from forced alignment of each sentence
// against its own audio, which keeps natural prosody while still recording
// where each word starts.
var probeSentences = []string{
	"The harbour lantern burned late while Mara studied the tide charts on the wall.",
	"Quilby the cartographer mapped the marshes past midnight for the Meridian Survey.",
	"Rain drummed the skylight as the attic clock counted eleven bells over Lisbon.",
	"She folded the ferry schedules into a paper crane and set it sailing at dawn.",
}

// probeWord marks where one generated word starts and ends in the clip.
type probeWord struct {
	Word  string
	Start float64
	End   float64
}

type batchWord struct {
	Text       string  `json:"text"`
	Start      int64   `json:"start"`
	End        int64   `json:"end"`
	Confidence float64 `json:"confidence"`
	Speaker    *string `json:"speaker"`
}

type batchEntity struct {
	Type  string `json:"entity_type"`
	Text  string `json:"text"`
	Start int64  `json:"start"`
	End   int64  `json:"end"`
}

type batchHighlightStamp struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

type batchHighlight struct {
	Count      int                   `json:"count"`
	Rank       float64               `json:"rank"`
	Text       string                `json:"text"`
	Timestamps []batchHighlightStamp `json:"timestamps"`
}

type batchTranscript struct {
	ID            string        `json:"id"`
	Status        string        `json:"status"`
	Text          *string       `json:"text"`
	Confidence    *float64      `json:"confidence"`
	AudioDuration *int64        `json:"audio_duration"`
	Words         []batchWord   `json:"words"`
	Entities      []batchEntity `json:"entities"`
	WebhookURL    *string       `json:"webhook_url"`
	WebhookStatus *int          `json:"webhook_status_code"`
	Highlights    *struct {
		Status  string           `json:"status"`
		Results []batchHighlight `json:"results"`
	} `json:"auto_highlights_result"`
	Understanding *struct {
		Request  json.RawMessage `json:"request"`
		Response json.RawMessage `json:"response"`
	} `json:"speech_understanding"`
	Failure *string `json:"error"`
}

// batchClient carries the key and the HTTP client for one probe run.
type batchClient struct {
	key string
	api *http.Client
}

// newBatchClient loads the key through the settings loader and returns a
// client for the probe. It logs only the key length, never the value.
func newBatchClient(t *testing.T) batchClient {
	t.Helper()
	loaded, _, err := settings.Load(context.Background())
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	key, err := loaded.Secrets.AssemblyAIAPIKey.Reveal()
	if err != nil {
		t.Fatalf("reveal assemblyai key: %v", err)
	}
	if len(key) == 0 {
		t.Fatal("assemblyai key resolved empty")
	}
	t.Logf("key length: %d", len(key))
	return batchClient{key: key, api: &http.Client{Timeout: 120 * time.Second}}
}

// call sends one JSON request with the key attached and decodes the reply.
func (c batchClient) call(t *testing.T, method, url string, body any, out any) int {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", c.key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.api.Do(req)
	if err != nil {
		t.Fatalf("call %s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if out != nil && resp.StatusCode < 400 && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("decode response: %v (body %.300s)", err, raw)
		}
	}
	if resp.StatusCode >= 400 {
		t.Logf("error body: %.500s", raw)
	}
	return resp.StatusCode
}

// synthSentence renders one sentence to a WAV file with the local voice.
// Piper is a local neural voice, so no network call and no key is involved.
func synthSentence(t *testing.T, dir, voice, sentence, out string) {
	t.Helper()
	piper := filepath.Join(dir, "piper-venv", "bin", "python")
	if _, err := os.Stat(piper); err != nil {
		t.Fatalf("piper venv missing: %v", err)
	}
	script := "from piper import PiperVoice, SynthesisConfig\n" +
		"import sys, wave\n" +
		"voice, sentence, out = sys.argv[1], sys.argv[2], sys.argv[3]\n" +
		"v = PiperVoice.load(voice)\n" +
		"cfg = SynthesisConfig(volume=1.0, length_scale=1.0, normalize_audio=False)\n" +
		"parts = []\n" +
		"for c in v.synthesize(sentence, cfg):\n" +
		"    parts.append(c.audio_int16_bytes)\n" +
		"audio = b\"\".join(parts)\n" +
		"with wave.open(out, \"wb\") as f:\n" +
		"    f.setnchannels(1)\n" +
		"    f.setsampwidth(2)\n" +
		"    f.setframerate(v.config.sample_rate)\n" +
		"    f.writeframes(audio)\n"
	cmd := exec.Command(piper, "-c", script, voice, sentence, out)
	cmd.Dir = dir
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("synthesize sentence: %v %.500s", err, raw)
	}
}

// readWAV decodes a mono 16 bit WAV file into samples plus its rate.
func readWAV(t *testing.T, path string) ([]int16, int) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read wav: %v", err)
	}
	if len(raw) < 44 || string(raw[0:4]) != "RIFF" {
		t.Fatalf("not a wav file: %s", path)
	}
	rate := int(binary.LittleEndian.Uint32(raw[24:28]))
	body := raw[44:]
	out := make([]int16, len(body)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(body[i*2:]))
	}
	return out, rate
}

// writeWAV encodes mono 16 bit samples at the given rate.
func writeWAV(t *testing.T, path string, pcm []int16, rate int) {
	t.Helper()
	buf := new(bytes.Buffer)
	for _, h := range []any{"RIFF", uint32(36 + len(pcm)*2), "WAVE", "fmt ", uint32(16), uint16(1), uint16(1), uint32(rate), uint32(rate * 2), uint16(2), uint16(16), "data", uint32(len(pcm) * 2)} {
		switch v := h.(type) {
		case string:
			buf.WriteString(v)
		case uint32:
			_ = binary.Write(buf, binary.LittleEndian, v)
		case uint16:
			_ = binary.Write(buf, binary.LittleEndian, v)
		}
	}
	for _, s := range pcm {
		_ = binary.Write(buf, binary.LittleEndian, s)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write wav: %v", err)
	}
}

// resample converts samples from one rate to another with linear mapping.
func resample(pcm []int16, from, to int) []int16 {
	if from == to {
		return pcm
	}
	n := int(int64(len(pcm)) * int64(to) / int64(from))
	out := make([]int16, n)
	for i := range out {
		src := float64(i) * float64(from) / float64(to)
		lo := int(src)
		hi := lo + 1
		if hi >= len(pcm) {
			out[i] = pcm[len(pcm)-1]
			continue
		}
		frac := src - float64(lo)
		out[i] = int16(float64(pcm[lo])*(1-frac) + float64(pcm[hi])*frac)
	}
	return out
}

// alignWords finds each word start by matching energy envelopes of the
// sentence audio against per-word renders. It returns word bounds in seconds
// relative to the sentence start.
func alignWords(t *testing.T, dir, voice string, sentence string, sentencePCM []int16, rate int) []probeWord {
	t.Helper()
	words := strings.Split(sentence, " ")
	tmpls := make([][]int16, len(words))
	for i, word := range words {
		wav := filepath.Join(dir, fmt.Sprintf("word-%d.wav", i))
		synthSentence(t, dir, voice, word, wav)
		pcm, wordRate := readWAV(t, wav)
		tmpls[i] = resample(pcm, wordRate, rate)
	}
	frame := rate / 100
	energy := func(pcm []int16, at int) float64 {
		var sum float64
		for i := 0; i < frame && at+i < len(pcm); i++ {
			v := float64(pcm[at+i]) / 32768
			sum += v * v
		}
		return sum / float64(frame)
	}
	sentEnv := make([]float64, len(sentencePCM)/frame+1)
	for i := range sentEnv {
		sentEnv[i] = energy(sentencePCM, i*frame)
	}
	bounds := make([]probeWord, 0, len(words))
	cursor := 0
	for i, word := range words {
		tmplEnv := make([]float64, len(tmpls[i])/frame+1)
		for j := range tmplEnv {
			tmplEnv[j] = energy(tmpls[i], j*frame)
		}
		best, bestScore := cursor, -1.0
		limit := len(sentEnv) - len(tmplEnv)
		if limit < cursor {
			limit = cursor
		}
		window := len(sentEnv) / 2
		if window < 50 {
			window = 50
		}
		end := cursor + window
		if end > limit {
			end = limit
		}
		for at := cursor; at <= end; at++ {
			if at+len(tmplEnv) > len(sentEnv) {
				break
			}
			var score float64
			for j := range tmplEnv {
				diff := sentEnv[at+j] - tmplEnv[j]
				score -= diff * diff
			}
			if score > bestScore {
				bestScore = score
				best = at
			}
		}
		advance := len(tmplEnv)
		if advance < 1 {
			advance = 1
		}
		bounds = append(bounds, probeWord{
			Word:  word,
			Start: float64(best*frame) / float64(rate),
			End:   float64((best+advance)*frame) / float64(rate),
		})
		cursor = best + advance
	}
	return bounds
}

// buildProbeClip synthesizes every sentence locally, aligns each word, and
// joins the sentences at 48 kHz with fixed gaps. It returns the clip path
// and the known word boundaries in seconds.
func buildProbeClip(t *testing.T, dir string) (string, []probeWord) {
	t.Helper()
	venv := filepath.Join(dir, "piper-venv")
	cmd := exec.Command("python3", "-m", "venv", venv)
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create venv: %v %.300s", err, raw)
	}
	cmd = exec.Command(filepath.Join(venv, "bin", "pip"), "install", "-q", "piper-tts")
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install piper: %v %.300s", err, raw)
	}
	cmd = exec.Command(filepath.Join(venv, "bin", "python"), "-m", "piper.download_voices", "en_US-lessac-medium", "--download-dir", dir)
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fetch voice: %v %.300s", err, raw)
	}
	voice := filepath.Join(dir, "en_US-lessac-medium.onnx")
	const rate = 48000
	const gapLen = rate * 7 / 10
	var clip []int16
	var bounds []probeWord
	pos := 0.0
	for si, sentence := range probeSentences {
		wav := filepath.Join(dir, fmt.Sprintf("sentence-%d.wav", si))
		synthSentence(t, dir, voice, sentence, wav)
		pcm, sentRate := readWAV(t, wav)
		for _, w := range alignWords(t, dir, voice, sentence, resample(pcm, sentRate, rate), rate) {
			bounds = append(bounds, probeWord{Word: w.Word, Start: pos + w.Start, End: pos + w.End})
		}
		pcm48 := resample(pcm, sentRate, rate)
		clip = append(clip, pcm48...)
		pos += float64(len(pcm48)) / rate
		clip = append(clip, make([]int16, gapLen)...)
		pos += float64(gapLen) / rate
	}
	path := filepath.Join(dir, "probe-48k.wav")
	writeWAV(t, path, clip, rate)
	return path, bounds
}

// uploadClip posts the WAV bytes to the upload endpoint and returns the URL.
func (c batchClient) uploadClip(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read clip: %v", err)
	}
	req, err := http.NewRequest("POST", batchProbeBaseURL+"/v2/upload", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("build upload: %v", err)
	}
	req.Header.Set("Authorization", c.key)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.api.Do(req)
	if err != nil {
		t.Fatalf("upload clip: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatalf("read upload reply: %v", err)
	}
	if resp.StatusCode >= 400 {
		t.Fatalf("upload failed: %d %.300s", resp.StatusCode, body)
	}
	var decoded struct {
		UploadURL string `json:"upload_url"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode upload reply: %v", err)
	}
	return decoded.UploadURL
}

// createTranscript starts one transcription and returns its id.
func (c batchClient) createTranscript(t *testing.T, params map[string]any) string {
	t.Helper()
	var decoded struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	code := c.call(t, "POST", batchProbeBaseURL+"/v2/transcript", params, &decoded)
	if code >= 400 {
		t.Fatalf("create transcript failed: %d", code)
	}
	t.Logf("transcript %s status %s", decoded.ID, decoded.Status)
	return decoded.ID
}

// pollTranscript waits for the transcript to finish and returns the shape.
func (c batchClient) pollTranscript(t *testing.T, id string) batchTranscript {
	t.Helper()
	deadline := time.Now().Add(10 * time.Minute)
	for {
		var tx batchTranscript
		code := c.call(t, "GET", batchProbeBaseURL+"/v2/transcript/"+id, nil, &tx)
		if code >= 400 {
			t.Fatalf("poll transcript failed: %d", code)
		}
		if tx.Status == "completed" || tx.Status == "error" {
			return tx
		}
		if time.Now().After(deadline) {
			t.Fatalf("transcript %s still %s after 10 minutes", id, tx.Status)
		}
		time.Sleep(5 * time.Second)
	}
}

// deleteTranscript removes one transcript by id.
func (c batchClient) deleteTranscript(t *testing.T, id string) {
	t.Helper()
	code := c.call(t, "DELETE", batchProbeBaseURL+"/v2/transcript/"+id, nil, nil)
	if code >= 400 {
		t.Fatalf("delete transcript failed: %d", code)
	}
}

// plainWord strips punctuation and case so generated words match transcripts.
func plainWord(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
}

func valueOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// logWordErrors aligns known bounds against returned words and logs the
// per-word start and end error in milliseconds. Spelling drifts log as
// mismatches instead of errors, so one mishearing skews no other word.
func logWordErrors(t *testing.T, bounds []probeWord, words []batchWord) {
	t.Helper()
	for _, b := range bounds {
		t.Logf("known %q start %.0f end %.0f", b.Word, b.Start*1000, b.End*1000)
	}
	ei, ri := 0, 0
	matched := 0
	for ei < len(bounds) && ri < len(words) {
		want := bounds[ei]
		got := words[ri]
		if plainWord(want.Word) == plainWord(got.Text) {
			startErr := want.Start*1000 - float64(got.Start)
			endErr := want.End*1000 - float64(got.End)
			t.Logf("word %q start_err %.0f end_err %.0f", want.Word, startErr, endErr)
			matched++
			ei++
			ri++
			continue
		}
		t.Logf("mismatch known %q (%.0f) returned %q (%d)", want.Word, want.Start*1000, got.Text, got.Start)
		if ei+1 < len(bounds) && plainWord(bounds[ei+1].Word) == plainWord(got.Text) {
			ei++
			continue
		}
		if ri+1 < len(words) && plainWord(want.Word) == plainWord(words[ri+1].Text) {
			ri++
			continue
		}
		ei++
		ri++
	}
	t.Logf("matched %d of %d known words", matched, len(bounds))
}

func TestBatchProbe(t *testing.T) {
	client := newBatchClient(t)
	dir := t.TempDir()
	clip, bounds := buildProbeClip(t, dir)
	info, err := os.Stat(clip)
	if err != nil {
		t.Fatalf("stat clip: %v", err)
	}
	t.Logf("clip bytes %d known words %d", info.Size(), len(bounds))

	uploadURL := client.uploadClip(t, clip)
	t.Logf("upload ok")

	id := client.createTranscript(t, map[string]any{
		"audio_url":        uploadURL,
		"speech_models":    []string{"universal-3-5-pro"},
		"entity_detection": true,
		"auto_highlights":  true,
		"speech_understanding": map[string]any{
			"request": map[string]any{
				"summarization": map[string]any{"summary_type": "bullets"},
			},
		},
	})
	tx := client.pollTranscript(t, id)
	if tx.Status != "completed" {
		t.Fatalf("transcript status %s failure %s", tx.Status, valueOrEmpty(tx.Failure))
	}
	t.Logf("text: %.500s", valueOrEmpty(tx.Text))
	if tx.AudioDuration != nil {
		t.Logf("audio duration seconds: %d", *tx.AudioDuration)
	}
	for i, w := range tx.Words {
		t.Logf("word %d %q start %d end %d conf %.3f", i, w.Text, w.Start, w.End, w.Confidence)
	}
	logWordErrors(t, bounds, tx.Words)
	for _, e := range tx.Entities {
		t.Logf("entity %s %q start %d end %d", e.Type, e.Text, e.Start, e.End)
	}
	if tx.Highlights != nil {
		for _, h := range tx.Highlights.Results {
			t.Logf("phrase %q rank %.4f count %d stamps %d", h.Text, h.Rank, h.Count, len(h.Timestamps))
		}
	}
	if tx.Understanding != nil {
		t.Logf("summary response: %.2000s", tx.Understanding.Response)
	}

	var paras struct {
		Paragraphs []struct {
			Text  string `json:"text"`
			Start int64  `json:"start"`
			End   int64  `json:"end"`
		} `json:"paragraphs"`
	}
	code := client.call(t, "GET", batchProbeBaseURL+"/v2/transcript/"+id+"/paragraphs", nil, &paras)
	if code >= 400 {
		t.Fatalf("paragraphs failed: %d", code)
	}
	t.Logf("paragraphs %d", len(paras.Paragraphs))
	for _, p := range paras.Paragraphs {
		t.Logf("paragraph start %d end %d text %.200s", p.Start, p.End, p.Text)
	}

	var gateway struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage map[string]any `json:"usage"`
	}
	code = client.call(t, "POST", batchProbeGatewayURL, map[string]any{
		"model": "qwen3.5-4b-32k-fast",
		"messages": []map[string]any{
			{"role": "user", "content": fmt.Sprintf("Split this transcript into chapters as JSON. Transcript:\n\n%s", valueOrEmpty(tx.Text))},
		},
		"max_tokens": 800,
	}, &gateway)
	if code >= 400 {
		t.Fatalf("gateway chapters failed: %d", code)
	}
	if len(gateway.Choices) > 0 {
		t.Logf("chapters: %.2000s", gateway.Choices[0].Message.Content)
	}
	t.Logf("gateway usage: %v", gateway.Usage)

	hookID := client.createTranscript(t, map[string]any{
		"audio_url":     uploadURL,
		"speech_models": []string{"universal-3-5-pro"},
		"webhook_url":   "http://127.0.0.1:18731/aai-hook",
	})
	hook := client.pollTranscript(t, hookID)
	t.Logf("webhook transcript status %s webhook code %v", hook.Status, hook.WebhookStatus)
	if hook.WebhookStatus != nil {
		t.Fatalf("local webhook delivered unexpectedly: %d", *hook.WebhookStatus)
	}
	t.Logf("no webhook delivery reached this machine, polling carries results")
	client.deleteTranscript(t, hookID)

	client.deleteTranscript(t, id)
	var after batchTranscript
	code = client.call(t, "GET", batchProbeBaseURL+"/v2/transcript/"+id, nil, &after)
	t.Logf("fetch after delete code %d text %.100s words %d", code, valueOrEmpty(after.Text), len(after.Words))
	if valueOrEmpty(after.Text) != "Deleted by user." || len(after.Words) != 0 {
		t.Fatalf("delete left content behind")
	}
}
