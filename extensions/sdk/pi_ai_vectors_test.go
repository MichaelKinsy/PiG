package sdk

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The vectors are test/parity/sdk-vectors/<module>/<export>.json: what Pi's own function returned at the pin for each case (README.md there). legacyGolden reads the ones this
// SDK's pi-ai helpers are tested with and arranges them as the tests below consume them.

const vectorRoot = "../../test/parity/sdk-vectors"

type vectorCase struct {
	Name   string            `json:"name"`
	Args   []json.RawMessage `json:"args"`
	Result json.RawMessage   `json:"result"`
	Throws *string           `json:"throws"`
}

func readVectors(t *testing.T, module, export string) []vectorCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(vectorRoot, module, export+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []vectorCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("%s/%s: %v", module, export, err)
	}
	if len(file.Cases) == 0 {
		t.Fatalf("%s/%s has no cases", module, export)
	}
	return file.Cases
}

// isUndefinedTag is the vector encoding of JavaScript's undefined, as a value or an argument.
func isUndefinedTag(raw json.RawMessage) bool {
	var compact bytes.Buffer
	return len(raw) == 0 || (json.Compact(&compact, raw) == nil && compact.String() == `{"$":"undefined"}`)
}

// vectorExports lists the exports under a module directory that have vectors.
func vectorExports(t *testing.T, module string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(vectorRoot, module, "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no vectors under %s: %v", module, err)
	}
	var exports []string
	for _, file := range files {
		exports = append(exports, strings.TrimSuffix(filepath.Base(file), ".json"))
	}
	sort.Strings(exports)
	return exports
}

// loadGolden returns every call of the plain pi-ai vectors (an argument or answer that is undefined has no value) and the envApiKeyAuth vectors.
func loadGolden(t *testing.T) (cases []goldenCase, auth goldenAuth) {
	t.Helper()
	for _, export := range vectorExports(t, "pi-ai") {
		if export == "envApiKeyAuth" || export == "createAssistantMessageEventStream" {
			continue
		}
		for _, c := range readVectors(t, "pi-ai", export) {
			args := make([]json.RawMessage, len(c.Args))
			for i, arg := range c.Args {
				args[i] = arg
				if isUndefinedTag(arg) {
					args[i] = json.RawMessage("null")
				}
			}
			var text *string
			if !isUndefinedTag(c.Result) {
				value := canonical(t, c.Result)
				text = &value
			}
			cases = append(cases, goldenCase{Fn: export, Args: args, Text: text})
		}
	}
	for _, c := range readVectors(t, "pi-ai", "envApiKeyAuth") {
		var env map[string]string
		_ = json.Unmarshal(c.Args[3], &env)
		var envVars []string
		_ = json.Unmarshal(c.Args[1], &envVars)
		var name string
		_ = json.Unmarshal(c.Args[0], &name)
		var answer struct {
			Result  json.RawMessage  `json:"result"`
			Seen    []string         `json:"seen"`
			Prompts []map[string]any `json:"prompts"`
		}
		_ = json.Unmarshal(c.Result, &answer)
		if string(c.Args[2]) == `"$login"` {
			var login struct {
				Result map[string]any `json:"result"`
			}
			_ = json.Unmarshal(c.Result, &login)
			auth.Login.Prompts, auth.Login.Result = answer.Prompts, login.Result
			continue
		}
		entry := struct {
			Name       string            `json:"name"`
			EnvVars    []string          `json:"envVars"`
			Credential json.RawMessage   `json:"credential"`
			Env        map[string]string `json:"env"`
			Seen       []string          `json:"seen"`
			Text       *string           `json:"text"`
		}{Name: name, EnvVars: envVars, Env: env, Seen: answer.Seen}
		if !isUndefinedTag(c.Args[2]) {
			entry.Credential = c.Args[2]
		}
		if string(answer.Result) != "null" {
			value := canonical(t, answer.Result)
			entry.Text = &value
		}
		auth.Cases = append(auth.Cases, entry)
	}
	return cases, auth
}

// ReadStoredCredential reads the credential an auth.json holds, in the key order of the file, as Pi's does (vectors: pi-coding-agent/readStoredCredential.json).
func TestReadStoredCredentialMatchesThePinnedPackage(t *testing.T) {
	for _, c := range readVectors(t, "pi-coding-agent", "readStoredCredential") {
		var provider string
		_ = json.Unmarshal(c.Args[0], &provider)
		path := filepath.Join(t.TempDir(), "auth.json")
		if !isUndefinedTag(c.Args[1]) {
			var text string
			_ = json.Unmarshal(c.Args[1], &text)
			if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		credential, err := ReadStoredCredential(provider, path)
		got := "null"
		if err != nil {
			// A stored value that is not an object is an error here where Pi returns it as it is; no vector holds one.
			t.Fatalf("%s: %v", c.Name, err)
		}
		if credential != nil {
			encoded, err := json.Marshal(credential)
			if err != nil {
				t.Fatal(err)
			}
			got = canonical(t, encoded)
		}
		var answer struct {
			Credential json.RawMessage `json:"credential"`
		}
		if err := json.Unmarshal(c.Result, &answer); err != nil {
			t.Fatal(err)
		}
		if want := canonical(t, answer.Credential); got != want {
			t.Errorf("%s: %s, want %s", c.Name, got, want)
		}
	}
}
