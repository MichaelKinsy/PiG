package coding

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

type storedCredentialProbe struct {
	Provider string  `json:"provider"`
	Path     *string `json:"path"`
}

type storedCredentialAnswer struct {
	Threw  bool    `json:"threw"`
	Absent bool    `json:"absent"`
	Truthy bool    `json:"truthy"`
	JSON   *string `json:"json"`
}

// readStoredCredential (core/auth-storage.ts) against pinned Pi: auth.json files that are valid, empty, corrupt, byte-order-marked or not objects; providers whose entries are credentials, JavaScript-falsy values, arrays or nested objects; paths absolute, with ~, as file:// URLs (percent-escaped or malformed), relative to the working directory, or the agent directory default.
func TestReadStoredCredentialMatchesPi(t *testing.T) {
	script, err := filepath.Abs("testdata/stored_credential.mjs")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	agent := filepath.Join(home, "agent")
	for _, d := range []string{agent, filepath.Join(home, "sp ace")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PIG_CODING_AGENT_DIR", agent)
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	t.Chdir(home)

	apiKey := `{"type":"api_key","key":"sk-test","env":{"A":"b"}}`
	oauth := `{"type":"oauth","refresh":"r","access":"a","expires":1760000000000,"extra":[1,2]}`
	contents := []string{
		`{"anthropic":` + apiKey + `,"openai":` + oauth + `}`,
		"\ufeff" + `{"anthropic":` + apiKey + `}`,
		`{"anthropic":null,"openai":false,"google":0,"zai":"","a":"text","b":7,"c":true,"d":[1,{"x":2}],"e":{},"f":-0,"g":0.0,"h":1e400}`,
		`{"anthropic":` + apiKey + `,"anthropic":` + oauth + `}`,
		`{"anthropic":` + apiKey + `,}`,
		`{"anthropic":` + apiKey,
		``,
		"   \n",
		`null`,
		`{}`,
		`{"é":` + apiKey + `,"\u00e9x":` + oauth + `}`,
		`{"anthropic":{"type":"api_key","key":"\ud83d\ude00 \u2028 <>&","n":1.50,"big":12345678901234567890}}`,
	}
	var probes []storedCredentialProbe
	rng := rand.New(rand.NewPCG(5, 11))
	providers := []string{"anthropic", "openai", "google", "zai", "a", "b", "c", "d", "e", "f", "g", "h", "é", "éx", "missing", ""}
	for i, content := range contents {
		if err := os.WriteFile(filepath.Join(home, fmt.Sprintf("auth%d.json", i)), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "sp ace", fmt.Sprintf("auth%d.json", i)), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(agent, "auth.json"), []byte(contents[0]), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, "dir.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A file URL names a path with forward slashes and a leading one: "file:///C:/dir" on Windows, where "file://C:\dir" has a host.
	urlPath := func(path string) string { return "/" + strings.TrimPrefix(filepath.ToSlash(path), "/") }
	pathFor := func(i int) *string {
		var p string
		switch rng.IntN(9) {
		case 0, 1:
			p = filepath.Join(home, fmt.Sprintf("auth%d.json", i))
		case 2:
			p = fmt.Sprintf("~/auth%d.json", i)
		case 3:
			p = fmt.Sprintf("auth%d.json", i)
		case 4:
			p = "file://" + urlPath(filepath.Join(home, fmt.Sprintf("auth%d.json", i)))
		case 5:
			p = "file://" + (&url.URL{Path: urlPath(filepath.Join(home, "sp ace", fmt.Sprintf("auth%d.json", i)))}).EscapedPath()
		case 6:
			p = []string{"~", "dir.json", "nowhere.json", "file://bad-host/x", "file:///%zz", "", "~x/auth0.json", "~/sp ace/auth0.json"}[rng.IntN(8)]
		case 7:
			return nil
		default:
			p = filepath.Join(home, "sp ace", fmt.Sprintf("auth%d.json", i))
		}
		return &p
	}
	for range 1500 {
		probes = append(probes, storedCredentialProbe{Provider: providers[rng.IntN(len(providers))], Path: pathFor(rng.IntN(len(contents)))})
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", script, pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "PI_CODING_AGENT_DIR="+agent)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var answers []storedCredentialAnswer
	if err := json.Unmarshal(output, &answers); err != nil {
		t.Fatal(err)
	}
	mismatches, present := 0, 0
	for i, probe := range probes {
		path := ""
		if probe.Path != nil {
			path = *probe.Path
		}
		var entry json.RawMessage
		if probe.Path == nil {
			entry = icodingagent.ReadStoredCredentialEntry(probe.Provider, "")
		} else {
			entry = icodingagent.ReadStoredCredentialEntryAt(probe.Provider, path)
		}
		var credential *ai.Credential
		if probe.Path == nil {
			credential = ReadStoredCredential(probe.Provider)
		} else {
			credential = ReadStoredCredential(probe.Provider, path)
		}
		answer := answers[i]
		// Pi returns the entry as written, falsy or not; Pig folds absent, undefined and falsy values into nil, which a caller testing the result treats alike.
		wantEntry := answer.Truthy
		bad := ""
		switch {
		case !wantEntry && credential != nil:
			bad = fmt.Sprintf("credential %+v, Pi %+v", credential, answer)
		case wantEntry != (entry != nil):
			bad = fmt.Sprintf("entry present %v, Pi %+v", entry != nil, answer)
		case wantEntry:
			present++
			var want, got any
			if err := json.Unmarshal([]byte(*answer.JSON), &want); err != nil {
				t.Fatal(err)
			}
			// A number beyond float64 is Infinity in Pi and null once serialized.
			if want == nil && jsInfinity(entry) {
				got = nil
			} else if err := json.Unmarshal(entry, &got); err != nil || !reflect.DeepEqual(want, got) {
				bad = fmt.Sprintf("entry %s, Pi %s", entry, *answer.JSON)
			}
			if object, ok := want.(map[string]any); ok && bad == "" {
				if kind, ok := object["type"].(string); ok && (credential == nil || string(credential.Type) != kind) {
					bad = fmt.Sprintf("credential %+v, Pi type %q", credential, kind)
				}
			}
		}
		if bad != "" {
			if mismatches++; mismatches <= 8 {
				t.Errorf("provider %q path %q: %s", probe.Provider, path, bad)
			}
		}
	}
	if mismatches > 8 {
		t.Errorf("%d of %d probes differ from Pi", mismatches, len(probes))
	}
	if present < 100 {
		t.Errorf("only %d probes read an entry", present)
	}
}

func jsInfinity(entry []byte) bool {
	var number float64
	err := json.Unmarshal(entry, &number)
	return err != nil && bytes.ContainsAny(entry, "0123456789") && entry[0] != '"' && entry[0] != '{' && entry[0] != '['
}
