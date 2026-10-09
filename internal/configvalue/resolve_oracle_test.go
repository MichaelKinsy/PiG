package configvalue

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/coding-agent/src/core/resolve-config-value.ts

// TestResolveConfigValueMatchesPi runs Pi 1.1.0's resolve-config-value over templates built from the characters that matter to its parser ($, !, {, },
// names, digits) and a few shell commands, with and without provider-scoped env, and compares every exported reader.
func TestResolveConfigValueMatchesPi(t *testing.T) {
	env := map[string]string{"PIGT_A": "alpha", "PIGT_B": "", "PIGT_C1": "c 1", "PIGT_D": "$PIGT_A"}
	pieces := []string{"$", "$$", "$!", "!", "{", "}", "${", "${PIGT_A}", "${PIGT_B}", "${PIGT_C1}", "${PIGT_MISSING}", "${1X}", "${}", "${ }", "$PIGT_A", "$PIGT_B", "$PIGT_D", "$PIGT_MISSING",
		"$PIGT_C1x", "$1", "$_", "$_PIGT_A", "-", " ", "x", "é", "\n", "$PIGT_A$PIGT_A", "$PIGT_A$PIGT_C1", "}$", "$-"}
	configs := []string{"", "literal", "$", "!", "$$", "$!x", "!echo", "!printf 'a b  '", "!printf '\\n\\n'", "!exit 3", "!echo hi; exit 1", "!printf  abc", "!  echo   spaced  ", "!!echo hi", "$!echo hi",
		"!echo '$$'", "!notacommand_pigt_xyz"}
	rng := rand.New(rand.NewSource(11))
	for range 3000 {
		var s []byte
		for range 1 + rng.Intn(6) {
			s = append(s, pieces[rng.Intn(len(pieces))]...)
		}
		configs = append(configs, string(s))
	}
	headers := map[string]string{"a": "$PIGT_A", "b": "${PIGT_B}", "c": "lit", "d": "!printf out", "e": "$PIGT_MISSING", "f": "!exit 1", "g": "$$"}
	input, err := json.Marshal(map[string]any{"configs": configs, "env": env, "headers": headers})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/resolve_config_value.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want struct {
		Configs []struct {
			Name       *string
			Names      []string
			Missing    []string
			Command    bool
			Configured bool
			Value      *string
			Thrown     struct {
				Ok  *string
				Err string
			}
		}
		Headers map[string]string
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	failures := 0
	report := func(format string, args ...any) {
		if failures++; failures <= 15 {
			t.Errorf(format, args...)
		}
	}
	for i, config := range configs {
		w := want.Configs[i]
		wantName := ""
		if w.Name != nil {
			wantName = *w.Name
		}
		if got := GetConfigValueEnvVarName(config); got != wantName {
			report("GetConfigValueEnvVarName(%q) = %q, Pi %q", config, got, wantName)
		}
		if got := GetConfigValueEnvVarNames(config); !slices.Equal(got, w.Names) {
			report("GetConfigValueEnvVarNames(%q) = %q, Pi %q", config, got, w.Names)
		}
		if got := GetMissingConfigValueEnvVarNames(config, env); !slices.Equal(got, w.Missing) {
			report("GetMissingConfigValueEnvVarNames(%q) = %q, Pi %q", config, got, w.Missing)
		}
		if got := IsCommandConfigValue(config); got != w.Command {
			report("IsCommandConfigValue(%q) = %v, Pi %v", config, got, w.Command)
		}
		if got := IsConfigValueConfigured(config, env); got != w.Configured {
			report("IsConfigValueConfigured(%q) = %v, Pi %v", config, got, w.Configured)
		}
		// Go's "" is Pi's undefined (a command that fails or prints nothing, or an unset variable).
		wantValue := ""
		if w.Value != nil {
			wantValue = *w.Value
		}
		if got := ResolveUncached(config, env); got != wantValue {
			report("ResolveUncached(%q) = %q, Pi %q", config, got, wantValue)
		}
		got, err := ResolveOrError(config, "the key", env)
		switch {
		case w.Thrown.Err != "":
			if err == nil || err.Error() != w.Thrown.Err {
				report("ResolveOrError(%q) = %q, %v; Pi throws %q", config, got, err, w.Thrown.Err)
			}
		case err != nil || w.Thrown.Ok == nil || got != *w.Thrown.Ok:
			report("ResolveOrError(%q) = %q, %v; Pi %v", config, got, err, w.Thrown.Ok)
		}
	}
	if got := ResolveHeaders(headers, env); !reflect.DeepEqual(got, want.Headers) && (len(got) != 0 || len(want.Headers) != 0) {
		report("ResolveHeaders = %v, Pi %v", got, want.Headers)
	}
	if failures > 15 {
		t.Errorf("%d differences from Pi", failures)
	}
}
