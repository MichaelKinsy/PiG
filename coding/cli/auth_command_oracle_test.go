package cli

// pi: packages/coding-agent/src/cli/auth-command.ts

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// cli/auth-command.ts parseAuthCommand, validateAuthCommandArgs (over parseArgs of the command's arguments) and getAuthCredential against
// pinned Pi, over generated argument vectors and Authorization header values.
func TestAuthCommandMatchesPi(t *testing.T) {
	tokens := []string{"auth", "check", "print-api-key", "print-bearer-token", "help", "", "--provider", "--model", "--json", "--credentials", "--no-refresh", "--min-expiry",
		"openai", "gpt-4o", " openai ", "30m", "1h", "500ms", "2S", "1H", "10", "m", "-1m", "1.5h", "1ms ", "１m", "99999999999999999999999h", "--api-key", "k", "--unknown",
		"--unknown=v", "@file", "msg", "-x", "--", "--no-such", "ſ", "1ſ"}
	r := rand.New(rand.NewPCG(11, 13))
	var probes []map[string]any
	var vectors [][]string
	for _, sub := range []string{"check", "print-api-key", "print-bearer-token", "help", "", "bogus"} {
		for _, token := range tokens {
			vectors = append(vectors, []string{"auth", sub}, []string{"auth", sub, token}, []string{"auth", sub, "--provider", "p", token}, []string{"auth", sub, "--min-expiry", token})
		}
	}
	vectors = append(vectors, []string{}, []string{"auth"}, []string{"x"}, []string{"check"})
	for range 3000 {
		n := 1 + r.IntN(6)
		vector := []string{"auth", []string{"check", "print-api-key", "print-bearer-token"}[r.IntN(3)]}
		for range n {
			vector = append(vector, tokens[r.IntN(len(tokens))])
		}
		vectors = append(vectors, vector)
	}
	for _, v := range vectors {
		probes = append(probes, map[string]any{"args": v})
	}
	type header map[string]any
	var credentials []map[string]any
	for _, value := range []string{"Bearer abc", "bearer abc", "BEARER  abc def", "Bearer", "Bearer ", "Bearer\tx", "Bearer\u00a0x", "Bearer\u2028x", "Bearer x\ny", "Basic abc", "", "Bearer \u3000tok", "Bearer\ufefftok"} {
		for _, name := range []string{"authorization", "Authorization", "AUTHORIZATION", "x-auth"} {
			credentials = append(credentials, map[string]any{"credential": map[string]any{"auth": map[string]any{"headers": header{name: value}}}})
		}
	}
	credentials = append(credentials, map[string]any{"credential": map[string]any{"auth": map[string]any{"apiKey": "sk", "headers": header{"authorization": "Bearer t"}}}},
		map[string]any{"credential": map[string]any{"auth": map[string]any{"headers": header{"authorization": nil}}}},
		map[string]any{"credential": map[string]any{"auth": map[string]any{"headers": header{"x": "y", "authorization": "Bearer second"}}}},
		map[string]any{"credential": map[string]any{"auth": map[string]any{}}})
	probes = append(probes, credentials...)
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/auth_command.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	// Pi's messages name the program `pi` (APP_NAME); this program is `pig`.
	output = bytes.ReplaceAll(output, []byte(`\"pi auth`), []byte(`\"pig auth`))
	var expected []map[string]any
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures := 0
	report := func(what any, got, want map[string]any) {
		if failures++; failures <= 6 {
			t.Errorf("%v:\n  Pig %v\n  Pi  %v", what, got, want)
		}
	}
	for i, vector := range vectors {
		got := map[string]any{}
		command, err := ParseAuthCommand(vector)
		switch {
		case err != nil:
			got["parseError"] = err.Error()
		case command == nil:
			got["none"] = true
		default:
			c := map[string]any{"kind": string(command.Kind), "args": command.Args, "json": command.JSON, "credentials": command.Credentials, "noRefresh": command.NoRefresh}
			if command.MinExpiryMs != nil {
				c["minExpiryMs"] = *command.MinExpiryMs
			}
			got["command"] = c
			flags := parseArgs(command.Args)
			target, err := ValidateAuthCommandArgs(flags, command.Args, command.Kind)
			if err != nil {
				got["validateError"] = err.Error()
			} else {
				tg := map[string]any{}
				if target.Provider != "" {
					tg["provider"] = target.Provider
				}
				if target.Model != "" {
					tg["model"] = target.Model
				}
				got["target"] = tg
			}
		}
		raw, _ := json.Marshal(got)
		var normalized map[string]any
		_ = json.Unmarshal(raw, &normalized)
		if !reflect.DeepEqual(normalized, expected[i]) {
			report(vector, normalized, expected[i])
		}
	}
	for j, probe := range credentials {
		var auth ai.AuthResult
		raw, _ := json.Marshal(probe["credential"])
		if err := json.Unmarshal(raw, &auth); err != nil {
			t.Fatal(err)
		}
		i := len(vectors) + j
		got := map[string]any{"credential": GetAuthCredential(&auth)}
		if !reflect.DeepEqual(got, expected[i]) {
			report(probe, got, expected[i])
		}
	}
	if failures > 6 {
		t.Errorf("%d probes differ from Pi", failures)
	}
}
