// Package settings loads the Reprise configuration for one environment.
//
// One TOML file holds inline settings and secret references. Each secret
// names a source and never carries a value. Two read an env file. The
// Gemini credential is a service account key, so it names a file source
// and the value is the key's contents. The loader resolves
// every secret at boot and returns a plan that names each source. Log the
// plan on start so the running process shows where each secret came from.
package settings

import (
	"context"
	"errors"
	"fmt"

	"github.com/nrynss/keel/config"
	"github.com/nrynss/keel/config/source"
)

// PathVar names the environment variable that holds the settings file path.
const PathVar = "REPRISE_CONFIG"

// FallbackPath is the settings file used when PathVar names nothing.
const FallbackPath = "config/reprise.local.toml"

// ErrSecret reports a secret that has no value after every reference
// failed. The wrapped error names the key, the source and the locator.
var ErrSecret = errors.New("settings: secret has no value")

// ErrSettings reports a settings file that the loader refused. The wrapped
// error carries the cause.
var ErrSettings = errors.New("settings: cannot load settings")

// Settings holds the Reprise configuration for one environment. Inline
// values come from the TOML file. Secrets resolve from references at load.
type Settings struct {
	// TranscriptionModel identifies the batch model for the user stem. The
	// file may carry the dotted marketing spelling. The batch client sends
	// the dashed wire id the provider accepts.
	TranscriptionModel string `toml:"transcription_model"`
	// EditorialModel identifies the model that proposes cuts and chapters.
	EditorialModel string `toml:"editorial_model"`
	// SessionMaxSeconds caps one live session in seconds.
	SessionMaxSeconds int `toml:"session_max_seconds"`
	// GuestMaxSessions caps how many sessions one guest may start.
	GuestMaxSessions int `toml:"guest_max_sessions"`
	// GuestMaxEpisodes caps how many episodes one guest may keep.
	GuestMaxEpisodes int `toml:"guest_max_episodes"`
	// DailySpendCents caps provider spend per day in cents.
	DailySpendCents int64 `toml:"daily_spend_cents"`
	// RenderConcurrency caps how many renders run at once.
	RenderConcurrency int `toml:"render_concurrency"`
	// DataDir holds the application state directory.
	DataDir string `toml:"data_dir"`
	// MediaDir holds the recorded and rendered media directory.
	MediaDir string `toml:"media_dir"`
	// PublicOrigin is the public base URL that serves the application.
	PublicOrigin string `toml:"public_origin"`
	// VertexProject names the Google Cloud project that bills Vertex AI.
	VertexProject string `toml:"vertex_project"`
	// VertexLocation names the Vertex AI region the client calls.
	VertexLocation string `toml:"vertex_location"`
	// Secrets holds the three secret references. Each resolves at load.
	Secrets Secrets `toml:"secrets"`
}

// Secrets holds the secret references. Every field resolves at load and
// stays out of tracked files.
type Secrets struct {
	// AssemblyAIAPIKey authorizes AssemblyAI calls from the server only.
	AssemblyAIAPIKey config.Secret `toml:"assemblyai_api_key" config:"required"`
	// GeminiCredential holds a Vertex AI service account key. Its value is
	// the key file's JSON, because Vertex authenticates a service account
	// and the box runs no metadata server to discover one.
	GeminiCredential config.Secret `toml:"gemini_credential" config:"required"`
	// SessionSigningKey signs the guest session cookies.
	SessionSigningKey config.Secret `toml:"session_signing_key" config:"required"`
}

// Load reads the settings file named by PathVar and falls back to
// FallbackPath. It resolves every secret and returns the settings with the
// resolution plan. Log Plan.String at boot. The plan names each source and
// never carries a value.
func Load(ctx context.Context) (Settings, config.Plan, error) {
	registry := config.NewRegistry()
	if err := source.RegisterDefaults(registry, source.Config{}); err != nil {
		return Settings{}, config.Plan{}, fmt.Errorf("%w: %w", ErrSettings, err)
	}
	var settings Settings
	plan, err := config.Load(ctx, &settings, config.Config{
		PathVar:  PathVar,
		Search:   []string{FallbackPath},
		Registry: registry,
	})
	if err != nil {
		if errors.Is(err, config.ErrResolve) || errors.Is(err, config.ErrRequired) {
			return Settings{}, config.Plan{}, fmt.Errorf("%w: %w", ErrSecret, err)
		}
		return Settings{}, config.Plan{}, fmt.Errorf("%w: %w", ErrSettings, err)
	}
	return settings, plan, nil
}
