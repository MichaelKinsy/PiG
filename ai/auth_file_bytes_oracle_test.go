package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/coding-agent/src/core/auth-storage.ts

// The bytes of auth.json after credential writes (modify and delete: `JSON.stringify({ ...current, [provider]: next }, null, 2)` and
// `delete current[provider]`): provider order kept, array-index provider names first, the value forms, and the members of each credential. The
// pinned AuthStorage and PiG's write the same scripted calls over the same starting file.
func TestAuthFileBytesMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(53))
	quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	chunks := []string{"a", "sk-", "é", "😀", "\u2028", "\u2029", "<&>", "\\", "\"", "\n", "\u007f", "\u0001", "x y", "$HOME", "!cmd"}
	str := func() string {
		var b strings.Builder
		for range 1 + rng.Intn(4) {
			b.WriteString(chunks[rng.Intn(len(chunks))])
		}
		return b.String()
	}
	providers := []string{"openai", "anthropic", "10", "2", "z", "a", "github-copilot", "é", "<&>"}
	initials := []func() string{
		func() string { return "" },
		func() string { return "{}" },
		func() string {
			return fmt.Sprintf(`{"zeta":{"type":"api_key","key":%s},"10":{"type":"api_key","key":"k"},"openai":{"type":"oauth","refresh":"r","access":"a","expires":1e3},"keep":{"type":"api_key","key":"x","env":{"B":"1","A":"2"}}}`, quote(str()))
		},
		func() string {
			return "\ufeff" + `{"anthropic":{"type":"oauth","refresh":"r","access":"a","expires":1700000000000.5}}`
		},
	}
	type script struct {
		Initial *string          `json:"initial"`
		Ops     []map[string]any `json:"ops"`
	}
	var scripts []script
	for range 400 {
		s := script{}
		if i := rng.Intn(len(initials)); i > 0 {
			text := initials[i]()
			s.Initial = &text
		}
		for range 1 + rng.Intn(6) {
			provider := providers[rng.Intn(len(providers))]
			switch rng.Intn(5) {
			case 4:
				shape := []string{"anthropic", "copilot", "kimi", "codex", "chatgpt", "radius"}[rng.Intn(6)]
				s.Ops = append(s.Ops, map[string]any{"op": "shape", "shape": shape, "provider": provider, "refresh": str(), "access": str(), "expires": rng.Intn(2000000000), "extra": str()})
			case 0:
				s.Ops = append(s.Ops, map[string]any{"op": "delete", "provider": provider})
			case 1:
				s.Ops = append(s.Ops, map[string]any{"op": "oauth", "provider": provider, "refresh": str(), "access": str(), "expires": rng.Intn(2000000000)})
			default:
				key := str()
				if rng.Intn(6) == 0 {
					key = "" // an empty key is stored as "key": "" (auth-storage.ts set of { type: "api_key", key: "" })
				}
				op := map[string]any{"op": "apiKey", "provider": provider, "key": key, "env": nil}
				if rng.Intn(3) == 0 {
					op["env"] = map[string]string{"Z": str(), "A": str(), "10": str()}
				}
				s.Ops = append(s.Ops, op)
			}
		}
		scripts = append(scripts, s)
	}
	input, err := json.Marshal(scripts)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/auth_file_bytes.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []*string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, s := range scripts {
		path := filepath.Join(t.TempDir(), "auth.json")
		if s.Initial != nil {
			if err := os.WriteFile(path, []byte(*s.Initial), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		storage, err := NewAuthStorage(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, op := range s.Ops {
			provider := op["provider"].(string)
			switch op["op"] {
			case "delete":
				err = storage.Delete(context.Background(), provider)
			case "shape":
				err = storage.Set(provider, oauthShapeCredential(t, op))
			case "oauth":
				err = storage.Set(provider, Credential{Type: CredentialOAuth, Refresh: op["refresh"].(string), Access: op["access"].(string), Expires: int64(op["expires"].(int))})
			default:
				credential := Credential{Type: CredentialAPIKey, Key: op["key"].(string)}
				if credential.Key == "" {
					// The login prompt keeps an empty key as a stored member (provider_login.go promptAPIKey).
					credential.Extra = map[string]json.RawMessage{"key": json.RawMessage(`""`)}
				}
				if env, ok := op["env"].(map[string]string); ok {
					credential.Env = env
				}
				err = storage.Set(provider, credential)
			}
			if err != nil {
				t.Fatalf("script %d: %v", i, err)
			}
		}
		raw, readErr := os.ReadFile(path)
		got := string(raw)
		if readErr != nil {
			got = "<none>"
		}
		expected := "<none>"
		if want[i] != nil {
			expected = *want[i]
		}
		if got != expected {
			if failures++; failures <= 4 {
				t.Errorf("script %d: initial %v ops %v\n PiG %q\n Pi  %q", i, s.Initial, s.Ops, got, expected)
			}
		}
	}
	if failures > 4 {
		t.Errorf("%d of %d auth files differ from Pi", failures, len(scripts))
	}
}

// oauthShapeCredential is the credential a provider's builder produces for the shape: the members the literal writes and the order the builder carries.
func oauthShapeCredential(t *testing.T, op map[string]any) Credential {
	t.Helper()
	extra, _ := json.Marshal(op["extra"])
	creds := OAuthCredentials{Refresh: op["refresh"].(string), Access: op["access"].(string), Expires: int64(op["expires"].(int))}
	switch op["shape"] {
	case "copilot":
		creds.Extra = map[string]json.RawMessage{"enterpriseUrl": extra, "availableModelIds": json.RawMessage(fmt.Sprintf(`[%s,"m"]`, extra))}
		creds.order = credentialKeyOrder{keys: copilotCredentialMembers}
	case "anthropic":
		creds.order = credentialKeyOrder{keys: refreshFirstCredentialMembers}
	case "kimi":
		creds.order = credentialKeyOrder{keys: accessFirstCredentialMembers}
	case "codex":
		creds.AccountID, creds.order = op["extra"].(string), credentialKeyOrder{keys: codexCredentialMembers}
	case "chatgpt":
		creds.Extra = map[string]json.RawMessage{"clientId": extra, "scopes": json.RawMessage(fmt.Sprintf(`["a",%s]`, extra))}
		creds.order = credentialKeyOrder{keys: chatgptCredentialMembers}
	case "radius":
		creds.Scope, creds.order = op["extra"].(string), credentialKeyOrder{keys: radiusCredentialMembers}
	}
	credential, err := credentialFromOAuth(creds)
	if err != nil {
		t.Fatal(err)
	}
	return credential
}
