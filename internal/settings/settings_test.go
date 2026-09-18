package settings

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Dummy secret values for tests. Each one is unique so a leak probe can
// find it again in captured output.
const (
	dummyAssemblyKey = "dummy-assembly-key-9f3k2"
	dummyGeminiCred  = "dummy-gemini-cred-7q2m8"
	dummySigningKey  = "dummy-signing-key-4z8w1"
)

// helperEnv marks a rerun of this test binary as a startup helper.
const helperEnv = "REPRISE_SETTINGS_HELPER"

// helperModeEnv selects what the helper does: load only, or print the plan.
const helperModeEnv = "REPRISE_SETTINGS_HELPER_MODE"

// TestMain reruns this binary as a startup helper when the marker is set.
// The helper calls Load and exits, so the parent can assert on the exit
// code and the captured output like a supervisor would.
func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) != "" {
		os.Exit(runHelper())
	}
	os.Exit(m.Run())
}

// runHelper loads the settings and exits. Mode plan prints the resolution
// plan on stdout. Any load failure goes to stderr. The exit code reports
// whether startup would proceed.
func runHelper() int {
	ctx := context.Background()
	switch os.Getenv(helperModeEnv) {
	case "plan":
		_, plan, err := Load(ctx)
		if err != nil {
			os.Stderr.WriteString("load: " + err.Error() + "\n")
			return 1
		}
		os.Stdout.WriteString(plan.String())
		return 0
	default:
		if _, _, err := Load(ctx); err != nil {
			os.Stderr.WriteString("load: " + err.Error() + "\n")
			return 1
		}
		return 0
	}
}

// runSelf starts this test binary as a helper. It returns the combined
// output and the exit code. An empty configPath leaves PathVar unset so
// the helper must fall back to the search path.
func runSelf(t *testing.T, mode, dir, configPath string) (string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Dir = dir
	env := make([]string, 0, len(os.Environ())+3)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, PathVar+"=") || strings.HasPrefix(kv, helperEnv) {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, helperEnv+"=1", helperModeEnv+"="+mode)
	if configPath != "" {
		env = append(env, PathVar+"="+configPath)
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("helper failed to run: %v", err)
		}
		code = exit.ExitCode()
	}
	return string(out), code
}

// writeEnv writes an env file with mode 0600 and returns its path.
func writeEnv(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "env")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeConfig writes a settings file and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "reprise.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// fullEnv holds all three dummy values.
func fullEnv() string {
	return "ASSEMBLYAI_API_KEY=" + dummyAssemblyKey + "\n" +
		"GEMINI_CREDENTIAL=" + dummyGeminiCred + "\n" +
		"SESSION_SIGNING_KEY=" + dummySigningKey + "\n"
}

// configDoc renders a valid settings document whose secrets read envPath.
func configDoc(envPath string) string {
	return fmt.Sprintf(`transcription_model = "universal-3.5-pro"
editorial_model = "gemini-2.5-flash"
session_max_seconds = 1800
guest_max_sessions = 10
guest_max_episodes = 10
daily_spend_cents = 2000
render_concurrency = 1
data_dir = "./data"
media_dir = "./media"
public_origin = "http://localhost:8080"

[secrets.assemblyai_api_key]
source = "env_file"
path = %q
var = "ASSEMBLYAI_API_KEY"

[secrets.gemini_credential]
source = "env_file"
path = %q
var = "GEMINI_CREDENTIAL"

[secrets.session_signing_key]
source = "env_file"
path = %q
var = "SESSION_SIGNING_KEY"
`, envPath, envPath, envPath)
}

// TestStartupStopsOnMissingSecret starts the helper against an env file
// that lacks one variable. Startup must stop, and the report must name
// the key, the source and the path.
func TestStartupStopsOnMissingSecret(t *testing.T) {
	envPath := writeEnv(t, "ASSEMBLYAI_API_KEY="+dummyAssemblyKey+"\n"+
		"GEMINI_CREDENTIAL="+dummyGeminiCred+"\n")
	configPath := writeConfig(t, configDoc(envPath))

	out, code := runSelf(t, "load", "", configPath)
	if code == 0 {
		t.Fatal("helper exited 0 with a missing secret, want nonzero")
	}
	for _, want := range []string{"session_signing_key", "env_file", envPath} {
		if !strings.Contains(out, want) {
			t.Fatalf("helper output %q lacks %q", out, want)
		}
	}
}

// TestStartupStopsOnGroupReadableEnv starts the helper against an env file
// that group members can read. Startup must stop, and the report must name
// the path.
func TestStartupStopsOnGroupReadableEnv(t *testing.T) {
	envPath := writeEnv(t, fullEnv())
	if err := os.Chmod(envPath, 0o640); err != nil {
		t.Fatal(err)
	}
	configPath := writeConfig(t, configDoc(envPath))

	out, code := runSelf(t, "load", "", configPath)
	if code == 0 {
		t.Fatal("helper exited 0 with a group-readable env file, want nonzero")
	}
	if !strings.Contains(out, envPath) {
		t.Fatalf("helper output %q lacks the env path %q", out, envPath)
	}
}

// TestBootLogShowsPlanWithoutValues captures the helper plan output. It
// must name every secret source while carrying none of the dummy values.
func TestBootLogShowsPlanWithoutValues(t *testing.T) {
	envPath := writeEnv(t, fullEnv())
	configPath := writeConfig(t, configDoc(envPath))

	out, code := runSelf(t, "plan", "", configPath)
	if code != 0 {
		t.Fatalf("helper exited %d, want 0: %s", code, out)
	}
	for _, want := range []string{
		"secrets.assemblyai_api_key",
		"secrets.gemini_credential",
		"secrets.session_signing_key",
		"env_file",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("plan output %q lacks %q", out, want)
		}
	}
	for _, secret := range []string{dummyAssemblyKey, dummyGeminiCred, dummySigningKey} {
		if strings.Contains(out, secret) {
			t.Fatalf("plan output carries a secret value %q", secret)
		}
	}
}

// TestLoadFallsBackToLocalFile runs the helper with no PathVar set from a
// directory that holds the fallback file. Startup must succeed through the
// search path alone.
func TestLoadFallsBackToLocalFile(t *testing.T) {
	envPath := writeEnv(t, fullEnv())
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(root, "config", "reprise.local.toml")
	if err := os.WriteFile(local, []byte(configDoc(envPath)), 0o600); err != nil {
		t.Fatal(err)
	}

	out, code := runSelf(t, "load", root, "")
	if code != 0 {
		t.Fatalf("helper exited %d through the fallback file, want 0: %s", code, out)
	}
}

// TestLoadResolvesSecrets loads a complete setup in process. Every inline
// setting must match, and every secret must reveal its dummy value.
func TestLoadResolvesSecrets(t *testing.T) {
	envPath := writeEnv(t, fullEnv())
	t.Setenv(PathVar, writeConfig(t, configDoc(envPath)))

	settings, plan, err := Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if settings.TranscriptionModel != "universal-3.5-pro" {
		t.Fatalf("transcription model = %q, want the file value", settings.TranscriptionModel)
	}
	if settings.SessionMaxSeconds != 1800 {
		t.Fatalf("session cap = %d, want the file value", settings.SessionMaxSeconds)
	}
	got, err := settings.Secrets.AssemblyAIAPIKey.Reveal()
	if err != nil {
		t.Fatal(err)
	}
	if got != dummyAssemblyKey {
		t.Fatal("assembly key revealed the wrong value")
	}
	got, err = settings.Secrets.GeminiCredential.Reveal()
	if err != nil {
		t.Fatal(err)
	}
	if got != dummyGeminiCred {
		t.Fatal("gemini credential revealed the wrong value")
	}
	got, err = settings.Secrets.SessionSigningKey.Reveal()
	if err != nil {
		t.Fatal(err)
	}
	if got != dummySigningKey {
		t.Fatal("signing key revealed the wrong value")
	}
	if !strings.Contains(plan.String(), "env_file") {
		t.Fatalf("plan %q names no source", plan.String())
	}
}

// TestLoaderRefusesLooseSecretsFile points a file source at a
// group-readable secrets file. The load must fail with the secret
// sentinel.
func TestLoaderRefusesLooseSecretsFile(t *testing.T) {
	envPath := writeEnv(t, fullEnv())
	secretPath := filepath.Join(t.TempDir(), "signing.key")
	if err := os.WriteFile(secretPath, []byte(dummySigningKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(secretPath, 0o640); err != nil {
		t.Fatal(err)
	}
	doc := configDoc(envPath)
	doc = strings.Replace(doc, "source = \"env_file\"\npath = "+fmt.Sprintf("%q", envPath)+"\nvar = \"SESSION_SIGNING_KEY\"",
		"source = \"file\"\npath = "+fmt.Sprintf("%q", secretPath), 1)
	t.Setenv(PathVar, writeConfig(t, doc))

	if _, _, err := Load(context.Background()); !errors.Is(err, ErrSecret) {
		t.Fatalf("load error = %v, want the secret sentinel", err)
	}
}

// TestLoaderRefusesInlineSecret writes a literal value where a reference
// belongs. The load must fail with the settings sentinel.
func TestLoaderRefusesInlineSecret(t *testing.T) {
	envPath := writeEnv(t, fullEnv())
	doc := configDoc(envPath)
	doc = strings.Replace(doc, "[secrets.assemblyai_api_key]\nsource = \"env_file\"\npath = "+
		fmt.Sprintf("%q", envPath)+"\nvar = \"ASSEMBLYAI_API_KEY\"",
		"[secrets]\nassemblyai_api_key = \"hardcoded-value\"", 1)
	t.Setenv(PathVar, writeConfig(t, doc))

	if _, _, err := Load(context.Background()); !errors.Is(err, ErrSettings) {
		t.Fatalf("load error = %v, want the settings sentinel", err)
	}
}

// TestLoaderRefusesUnknownKey adds a key the struct has no field for. The
// load must fail with the settings sentinel.
func TestLoaderRefusesUnknownKey(t *testing.T) {
	envPath := writeEnv(t, fullEnv())
	t.Setenv(PathVar, writeConfig(t, configDoc(envPath)+"\nunknown_setting = 1\n"))

	if _, _, err := Load(context.Background()); !errors.Is(err, ErrSettings) {
		t.Fatalf("load error = %v, want the settings sentinel", err)
	}
}

// TestLoaderRefusesQuotedEnvValue wraps one env value in quotes. The load
// must fail with the secret sentinel.
func TestLoaderRefusesQuotedEnvValue(t *testing.T) {
	envPath := writeEnv(t, "ASSEMBLYAI_API_KEY=\""+dummyAssemblyKey+"\"\n"+
		"GEMINI_CREDENTIAL="+dummyGeminiCred+"\n"+
		"SESSION_SIGNING_KEY="+dummySigningKey+"\n")
	t.Setenv(PathVar, writeConfig(t, configDoc(envPath)))

	if _, _, err := Load(context.Background()); !errors.Is(err, ErrSecret) {
		t.Fatalf("load error = %v, want the secret sentinel", err)
	}
}
