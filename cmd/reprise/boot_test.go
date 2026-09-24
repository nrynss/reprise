package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nrynss/keel/job"
	jobsqlitestore "github.com/nrynss/keel/job/sqlitestore"
	keelsqlite "github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/broker"
	"github.com/nrynss/reprise/internal/episode"
	"github.com/nrynss/reprise/internal/settings"
)

// bootProbeRoute is the route the probe feature mounts through its hook.
const bootProbeRoute = "/api/boot-probe"

// bootProbeKind is the job kind the probe feature registers.
const bootProbeKind = "boot_probe"

// bootSettings writes a settings file for one boot under dir and loads it
// the way the process does. Every secret reads a local file. The Vertex
// key is a fresh service account key whose token endpoint refuses at
// once, so nothing the boot builds can reach the network.
func bootSettings(t *testing.T, dir string) settings.Settings {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("encode key: %v", err)
	}
	account, err := json.Marshal(map[string]string{
		"type":           "service_account",
		"project_id":     "boot-probe",
		"private_key_id": "boot-probe-key",
		"private_key":    string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email":   "boot-probe@boot-probe.iam.gserviceaccount.com",
		"client_id":      "1",
		"token_uri":      "http://127.0.0.1:1/token",
	})
	if err != nil {
		t.Fatalf("encode service account: %v", err)
	}
	write := func(name string, body []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return path
	}
	assembly := write("assembly.key", []byte("boot-probe-assembly-key"))
	signing := write("signing.key", []byte("boot-probe-signing-key"))
	vertex := write("vertex.json", account)
	doc := `transcription_model = "universal-3.5-pro"
editorial_model = "gemini-2.5-flash"
session_max_seconds = 1800
guest_max_sessions = 10
guest_max_episodes = 10
daily_spend_cents = 2000
render_concurrency = 1
data_dir = "` + filepath.Join(dir, "data") + `"
media_dir = "` + filepath.Join(dir, "media") + `"
vertex_project = "boot-probe"
vertex_location = "us-central1"
public_origin = "http://localhost:8080"

[secrets.assemblyai_api_key]
source = "file"
path = "` + assembly + `"

[secrets.gemini_credential]
source = "file"
path = "` + vertex + `"

[secrets.session_signing_key]
source = "file"
path = "` + signing + `"
`
	for _, sub := range []string{"data", "media"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatalf("make %s: %v", sub, err)
		}
	}
	t.Setenv(settings.PathVar, write("reprise.toml", []byte(doc)))
	loaded, _, err := settings.Load(context.Background())
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	return loaded
}

// openBootDB opens a second handle on the database the boot wired.
func openBootDB(t *testing.T, loaded settings.Settings) *keelsqlite.DB {
	t.Helper()
	db, err := keelsqlite.Open(t.Context(), keelsqlite.Config{Path: filepath.Join(loaded.DataDir, "reprise.db")})
	if err != nil {
		t.Fatalf("open boot database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// plantShippableEpisode leaves one episode analysing on a render whose
// analysis, cover and marking passes all finished. The next advance ships
// it without starting anything. The state moves last, so an advance that
// runs early finds nothing to do.
func plantShippableEpisode(t *testing.T, db *keelsqlite.DB) string {
	t.Helper()
	ctx := t.Context()
	const owner = "owner-boot-probe"
	now := time.Now().UnixMilli()
	if _, err := db.Writer().ExecContext(ctx,
		`INSERT INTO users (id, kind, created_at, last_seen_at) VALUES (?, 'guest', ?, ?)`,
		owner, now, now); err != nil {
		t.Fatalf("insert owner: %v", err)
	}
	diary, err := broker.NewSQLiteDiary(db)
	if err != nil {
		t.Fatalf("open diary: %v", err)
	}
	episodeID, _, err := diary.CreateEpisodeAndSession(ctx, owner, 1800)
	if err != nil {
		t.Fatalf("create episode: %v", err)
	}
	if _, err := db.Writer().ExecContext(ctx,
		`INSERT INTO renders (id, owner_id, episode_id, input_hash, opus_media_id, aac_media_id, loudness)
		 VALUES ('render-boot', ?, ?, 'hash-boot', 'opus-boot', 'aac-boot', -16)`, owner, episodeID); err != nil {
		t.Fatalf("seed render: %v", err)
	}
	store, err := jobsqlitestore.Open(ctx, jobsqlitestore.Config{DB: db})
	if err != nil {
		t.Fatalf("open job store: %v", err)
	}
	detail, err := json.Marshal(episodeDescriptor{OwnerID: owner, EpisodeID: episodeID, RenderID: "render-boot"})
	if err != nil {
		t.Fatalf("encode linkage: %v", err)
	}
	for _, kind := range []string{kindAnalysis, kindCover, kindMemory} {
		id := "job-boot-" + kind
		if err := store.Create(ctx, job.Record{
			ID: id, Kind: kind, Status: job.StatusRunning, Attempt: 1, RootID: id,
			Progress:  job.Progress{Stage: "start", Detail: detail},
			UpdatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("create %s job: %v", kind, err)
		}
		if err := store.Finish(ctx, job.Record{ID: id, Status: job.StatusDone, UpdatedAt: time.Now()}); err != nil {
			t.Fatalf("finish %s job: %v", kind, err)
		}
	}
	if _, err := db.Writer().ExecContext(ctx,
		`UPDATE episodes SET state = ? WHERE id = ?`, string(episode.StateAnalysing), episodeID); err != nil {
		t.Fatalf("plant analysing: %v", err)
	}
	return episodeID
}

// TestBootWiresEveryHook boots the real route wiring over a settings file
// and a probe feature. The boot must mount the feature routes, run the
// analysis migration, and start the advance loop. A second boot whose
// feature reuses a core kind must refuse, which proves the feature kinds
// reach the runner.
func TestBootWiresEveryHook(t *testing.T) {
	loaded := bootSettings(t, t.TempDir())
	mounted := 0
	probe := feature{
		name: "probe",
		kinds: func(kindWiring) (map[string]job.Kind, error) {
			return map[string]job.Kind{bootProbeKind: {Limit: 1}}, nil
		},
		mount: func(_ context.Context, w routeWiring) error {
			mounted++
			if w.Runner == nil || w.DB == nil || w.Gate == nil || w.Guests == nil || w.Media == nil {
				t.Error("mount hook received an incomplete wiring")
			}
			w.Mux.HandleFunc("GET "+bootProbeRoute, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})
			return nil
		},
	}
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	mux := http.NewServeMux()
	if _, err := wireAPI(ctx, mux, loaded, bootPlan{features: []feature{probe}, advancePeriod: 10 * time.Millisecond}); err != nil {
		t.Fatalf("boot: %v", err)
	}
	if mounted != 1 {
		t.Fatalf("mount hook calls = %d, want one", mounted)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, bootProbeRoute, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("feature route status = %d, want %d", rec.Code, http.StatusNoContent)
	}

	db := openBootDB(t, loaded)
	var links int
	if err := db.Reader().QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM rendered_sources`).Scan(&links); err != nil {
		t.Fatalf("read the analysis schema the boot migrated: %v", err)
	}

	episodeID := plantShippableEpisode(t, db)
	deadline := time.Now().Add(2 * time.Minute)
	for {
		state, err := episode.Current(t.Context(), db, episodeID)
		if err != nil {
			t.Fatalf("read state: %v", err)
		}
		if state == episode.StateReady {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("state = %s, want the advance loop to ship the episode", state)
		}
		time.Sleep(10 * time.Millisecond)
	}

	clash := feature{
		name: "clash",
		kinds: func(kindWiring) (map[string]job.Kind, error) {
			return map[string]job.Kind{kindRender: {Limit: 4}}, nil
		},
	}
	again := bootSettings(t, t.TempDir())
	_, err := wireAPI(ctx, http.NewServeMux(), again, bootPlan{features: []feature{clash}})
	if !errors.Is(err, errDuplicateKind) {
		t.Fatalf("clashing boot error = %v, want errDuplicateKind", err)
	}
}
