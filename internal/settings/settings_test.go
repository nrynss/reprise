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

// fullEnv holds the two dummy values the env file carries. The Gemini
// credential is a key file, so it is not one of them.
func fullEnv() string {
	return "ASSEMBLYAI_API_KEY=" + dummyAssemblyKey + "\n" +
		"SESSION_SIGNING_KEY=" + dummySigningKey + "\n"
}

// writeKeyFile writes a dummy service account key at mode 0600 and returns
// its path. The file source reads the whole file as the secret.
func writeKeyFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gemini-sa.json")
	if err := os.WriteFile(path, []byte(dummyGeminiCred), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// configDoc renders a valid settings document whose env secrets read
// envPath and whose Gemini credential reads a key file.
func configDoc(envPath string) string {
	return configDocWithKey(envPath, "")
}

// configDocWithKey renders the same document with an explicit key path. An
// empty keyPath writes a file beside the env file, which keeps the callers
// that do not care about the key unchanged.
func configDocWithKey(envPath, keyPath string) string {
	if keyPath == "" {
		keyPath = filepath.Join(filepath.Dir(envPath), "gemini-sa.json")
		_ = os.WriteFile(keyPath, []byte(dummyGeminiCred), 0o600)
	}
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
vertex_project = "reprise-test"
vertex_location = "us-central1"

[secrets.assemblyai_api_key]
source = "env_file"
path = %q
var = "ASSEMBLYAI_API_KEY"

[secrets.gemini_credential]
source = "file"
path = %q

[secrets.session_signing_key]
source = "env_file"
path = %q
var = "SESSION_SIGNING_KEY"
`, envPath, keyPath, envPath)
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
		"file",
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

// TestStartupStopsOnMissingKeyFile starts the helper against a settings
// file whose Gemini key path names nothing. Startup must stop, and the
// report must name the key, the source and the path.
func TestStartupStopsOnMissingKeyFile(t *testing.T) {
	envPath := writeEnv(t, fullEnv())
	keyPath := filepath.Join(t.TempDir(), "absent-sa.json")
	configPath := writeConfig(t, configDocWithKey(envPath, keyPath))

	out, code := runSelf(t, "load", "", configPath)
	if code == 0 {
		t.Fatal("helper exited 0 with a missing key file, want nonzero")
	}
	for _, want := range []string{"gemini_credential", "file", keyPath} {
		if !strings.Contains(out, want) {
			t.Fatalf("helper output %q lacks %q", out, want)
		}
	}
}

// TestStartupStopsOnGroupReadableKeyFile starts the helper against a key
// file that group members can read. A service account key is a credential,
// so startup must stop and name the path.
func TestStartupStopsOnGroupReadableKeyFile(t *testing.T) {
	envPath := writeEnv(t, fullEnv())
	keyPath := writeKeyFile(t)
	if err := os.Chmod(keyPath, 0o640); err != nil {
		t.Fatal(err)
	}
	configPath := writeConfig(t, configDocWithKey(envPath, keyPath))

	out, code := runSelf(t, "load", "", configPath)
	if code == 0 {
		t.Fatal("helper exited 0 with a group-readable key file, want nonzero")
	}
	if !strings.Contains(out, keyPath) {
		t.Fatalf("helper output %q lacks the key path %q", out, keyPath)
	}
}

// repoRoot walks up from the test's directory to the module root, which is
// the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test directory")
		}
		dir = parent
	}
}

// TestShippedFilesAgreeOnSecretNames reads the example env file and the
// local settings file and pins that they name the same env variables. A
// name that drifts in one file alone is a startup failure nobody sees
// until boot.
func TestShippedFilesAgreeOnSecretNames(t *testing.T) {
	root := repoRoot(t)
	example, err := os.ReadFile(filepath.Join(root, ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	local, err := os.ReadFile(filepath.Join(root, "config", "reprise.local.toml"))
	if err != nil {
		t.Fatal(err)
	}
	inExample := map[string]bool{}
	for _, line := range strings.Split(string(example), "\n") {
		if name, _, found := strings.Cut(line, "="); found && !strings.HasPrefix(line, "#") {
			inExample[strings.TrimSpace(name)] = true
		}
	}
	wanted := map[string]bool{}
	for _, line := range strings.Split(string(local), "\n") {
		if strings.HasPrefix(line, "var = ") {
			wanted[strings.Trim(strings.TrimPrefix(line, "var = "), `"`)] = true
		}
	}
	if len(wanted) == 0 {
		t.Fatal("the local settings file names no env variable")
	}
	for name := range wanted {
		if !inExample[name] {
			t.Errorf("settings read %s and .env.example does not list it", name)
		}
	}
	for name := range inExample {
		if !wanted[name] {
			t.Errorf(".env.example lists %s and no secret reads it", name)
		}
	}
}
