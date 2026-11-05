package cli

// pi: packages/ai/src/cli.ts

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Runs Pi's packages/ai/src/cli.ts as-is (Node, types stripped; testdata/ai_cli_oracle_hooks.mjs swaps its provider list for three
// scripted OAuth providers) for `login <provider>` and runs PiG's `login` command over the same scripts, in an empty working
// directory with the same stdin. Pi and PiG must print the same stdout and stderr, exit alike, and leave the same auth.json:
// readable by its owner only, holding the credential the flow returned beside the entries that were already there.
//
// Not compared: the provider menu `login` prints without a provider, `list` and `help`, which PiG replaces with `login --list` and
// `--help` over every registered auth target, and an unreadable auth.json, which cli.ts discards and overwrites while PiG's
// credential store refuses to replace it.

type aiCLIScript string

const (
	aiCLIText    aiCLIScript = "oracle-text"
	aiCLIAuthURL aiCLIScript = "oracle-auth-url"
	aiCLISelect  aiCLIScript = "oracle-select"
)

var aiCLIUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// aiCLIProvider is the Go twin of one script of testdata/ai_cli_oracle_providers.mjs.
type aiCLIProvider struct{ script aiCLIScript }

func (p aiCLIProvider) ID() string               { return string(p.script) }
func (p aiCLIProvider) Name() string             { return "Oracle " + string(p.script) }
func (p aiCLIProvider) UsesCallbackServer() bool { return false }
func (p aiCLIProvider) RefreshToken(ai.OAuthCredentials) (ai.OAuthCredentials, error) {
	return ai.OAuthCredentials{}, errors.New("not used")
}
func (p aiCLIProvider) GetAPIKey(creds ai.OAuthCredentials) string { return creds.Access }

func (p aiCLIProvider) Login(callbacks ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	credential := func(answer string) ai.OAuthCredentials {
		return ai.OAuthCredentials{Access: "access:" + answer, Refresh: "refresh-token", Expires: 4102444800000}
	}
	switch p.script {
	case aiCLIText:
		callbacks.OnProgress("Contacting the stub...")
		answer, err := callbacks.OnPrompt(ai.OAuthPrompt{Message: "Enter token", Placeholder: "tok-123"})
		if err != nil {
			return ai.OAuthCredentials{}, err
		}
		callbacks.OnDeviceCode(ai.OAuthDeviceCodeInfo{UserCode: "ABCD-1234", VerificationURI: "https://example.test/device"})
		callbacks.OnProgress("device id is a UUID: " + strconv.FormatBool(aiCLIUUID.MatchString(callbacks.GetDeviceID())))
		return credential(answer), nil
	case aiCLIAuthURL:
		callbacks.OnAuth(ai.OAuthAuthInfo{URL: "https://example.test/authorize", Instructions: "Approve the request."})
		callbacks.OnAuth(ai.OAuthAuthInfo{URL: "https://example.test/second"})
		callbacks.OnProgress("waiting")
		return credential("none"), nil
	default:
		answer, err := callbacks.OnSelect(ai.OAuthSelectPrompt{Message: "Pick an account", Options: []ai.OAuthSelectOption{{ID: "alpha", Label: "Alpha"}, {ID: "beta", Label: "Beta"}}})
		if err != nil {
			return ai.OAuthCredentials{}, err
		}
		return credential(answer), nil
	}
}

type aiCLICase struct {
	name     string
	provider string
	stdin    string
	// existing is the auth.json the working directory starts with ("" for none); it is created world-readable.
	existing string
}

type aiCLIOutcome struct {
	Stdout string
	Stderr string
	Code   int
	// Auth is auth.json parsed (nil when absent) and Mode its permission bits.
	Auth map[string]map[string]any
	Mode os.FileMode
}

const aiCLIExistingAuth = `{"other":{"type":"oauth","access":"a","refresh":"r","expires":1}}`

func aiCLICases() []aiCLICase {
	return []aiCLICase{
		{name: "text prompt, device code and progress", provider: "oracle-text", stdin: "secret-token\n"},
		{name: "empty text answer", provider: "oracle-text", stdin: "\n"},
		{name: "auth urls with and without instructions", provider: "oracle-auth-url"},
		{name: "select the second option", provider: "oracle-select", stdin: "2\n"},
		{name: "select an unlisted option", provider: "oracle-select", stdin: "9\n"},
		{name: "select a non-number", provider: "oracle-select", stdin: "x\n"},
		{name: "select an empty answer", provider: "oracle-select", stdin: "\n"},
		{name: "select after JavaScript-only whitespace, ignoring trailing text", provider: "oracle-select", stdin: "\u00a0\ufeff2 trailing\n"},
		{name: "select with a plus sign and a fraction", provider: "oracle-select", stdin: "+1.9\n"},
		{name: "unknown provider", provider: "nope"},
		{name: "api-key-only provider is not an OAuth provider", provider: "oracle-api-key"},
		{name: "existing auth.json is kept and made private", provider: "oracle-auth-url", existing: aiCLIExistingAuth},
	}
}

func readAICLIAuth(t *testing.T, dir string) (auth map[string]map[string]any, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(dir, "auth.json")
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0
	}
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &auth); err != nil {
		t.Fatalf("auth.json is not a credential map: %v\n%s", err, data)
	}
	return auth, info.Mode().Perm()
}

func seedAICLIAuth(t *testing.T, dir, existing string) {
	t.Helper()
	if existing == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "auth.json"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runPiAICLI(t *testing.T, c aiCLICase) aiCLIOutcome {
	t.Helper()
	root := testenv.ModuleRoot(t)
	dir := t.TempDir()
	seedAICLIAuth(t, dir, c.existing)
	testdata, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	// --import takes a module specifier, not a path: Node reads D:\... as a URL with scheme "d:" and refuses it, so it needs a file:// URL.
	hooks := nodeurl.PathToFileURL(filepath.Join(testdata, "ai_cli_oracle_hooks.mjs"), runtime.GOOS == "windows")
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning",
		"--import", hooks,
		filepath.Join(root, ".upstream", "current", "packages", "ai", "src", "cli.ts"), "login", c.provider)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PI_AI_CLI_PROVIDERS="+filepath.Join(testdata, "ai_cli_oracle_providers.mjs"))
	cmd.Stdin = strings.NewReader(c.stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("Pi cli.ts: %v\n%s", err, &stderr)
		}
		code = exit.ExitCode()
	}
	auth, mode := readAICLIAuth(t, dir)
	return aiCLIOutcome{Stdout: stdout.String(), Stderr: stderr.String(), Code: code, Auth: auth, Mode: mode}
}

func runPiGAICLI(t *testing.T, c aiCLICase) aiCLIOutcome {
	t.Helper()
	dir := t.TempDir()
	seedAICLIAuth(t, dir, c.existing)
	t.Chdir(dir)
	t.Setenv("PIG_CODING_AGENT_DIR", ".")
	for _, script := range []aiCLIScript{aiCLIText, aiCLIAuthURL, aiCLISelect} {
		provider := aiCLIProvider{script}
		ai.RegisterOAuthProvider(provider.ID(), provider)
		t.Cleanup(func() { ai.UnregisterOAuthProvider(provider.ID()) })
	}
	stdout, stderr, code := captureWithStdin(t, c.stdin, func() int { return runLoginCommand([]string{"login", c.provider}) })
	auth, mode := readAICLIAuth(t, dir)
	return aiCLIOutcome{Stdout: stdout, Stderr: stderr, Code: code, Auth: auth, Mode: mode}
}

func TestAICLILoginMatchesPi(t *testing.T) {
	for _, c := range aiCLICases() {
		t.Run(c.name, func(t *testing.T) {
			pi := runPiAICLI(t, c)
			pig := runPiGAICLI(t, c)
			if pig.Stdout != pi.Stdout {
				t.Errorf("stdout:\nPiG %q\nPi  %q", pig.Stdout, pi.Stdout)
			}
			if pig.Stderr != pi.Stderr {
				t.Errorf("stderr:\nPiG %q\nPi  %q", pig.Stderr, pi.Stderr)
			}
			if pig.Code != pi.Code {
				t.Errorf("exit code: PiG %d, Pi %d", pig.Code, pi.Code)
			}
			if pig.Mode != pi.Mode {
				t.Errorf("auth.json mode: PiG %v, Pi %v", pig.Mode, pi.Mode)
			}
			piAuth, _ := json.Marshal(pi.Auth)
			pigAuth, _ := json.Marshal(pig.Auth)
			if !bytes.Equal(pigAuth, piAuth) {
				t.Errorf("auth.json:\nPiG %s\nPi  %s", pigAuth, piAuth)
			}
		})
	}
}
