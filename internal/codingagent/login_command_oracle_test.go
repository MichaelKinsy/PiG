package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// interactive-mode.ts handleLoginCommand and findLoginProviderOptions against pinned Pi: `/login <ref>` matches the trimmed, lower-cased reference with
// JavaScript's toLowerCase (dotted İ, final sigma) against each provider's id and name, then starts the one match, opens the auth-type menu for several
// auth types of one provider, or opens the provider selector with the reference as its search.
func TestLoginCommandMatchesPi(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 17))
	names := []string{"OpenAI", "Anthropic", "İstanbul AI", "ΣΟΦΟΣ", "ΟΔΥΣΣΕΥΣ", "Straße", "GitHub Copilot", "zai", "Radius", " padded ", "a b", "ǅ-lab"}
	ids := []string{"openai", "anthropic", "istanbul", "sophos", "odysseus", "strasse", "github-copilot", "zai", "radius", "OpenAI", "ΣΟΦΟΣ", "x"}
	refs := []string{"openai", "OPENAI", " openai ", "OpenAI\u00a0", "\ufeffopenai", "i̇stanbul ai", "İSTANBUL AI", "istanbul ai", "ISTANBUL AI", "σοφος", "ΣΟΦΟΣ", "σοφοσ", "ὀδυσσεύς", "ΟΔΥΣΣΕΥΣ", "οδυσσευς", "οδυσσευσ", "straße", "STRASSE", "strasse", "github copilot", "github-copilot", "GITHUB-COPILOT", "zai", "ZAI", "radius", "padded", "a b", "A B", "ǆ-lab", "Ǆ-LAB", "ǅ-lab", "nothing", "x", "\u0085x"}
	type provider struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		AuthType string `json:"authType"`
	}
	type probe struct {
		Providers []provider `json:"providers"`
		Ref       string     `json:"ref"`
	}
	var probes []probe
	for range 4000 {
		var providers []provider
		for range 1 + r.IntN(6) {
			providers = append(providers, provider{ids[r.IntN(len(ids))], names[r.IntN(len(names))], []string{"oauth", "api_key"}[r.IntN(2)]})
		}
		ref := refs[r.IntN(len(refs))]
		if r.IntN(3) == 0 && len(providers) > 0 {
			chosen := providers[r.IntN(len(providers))]
			ref = []string{chosen.ID, chosen.Name}[r.IntN(2)]
		}
		probes = append(probes, probe{providers, widthx.JSTrim(ref)})
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/login_command.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want [][][]any
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	failures, outcomes := 0, map[string]int{}
	for i, p := range probes {
		var got [][]any
		sc := &SlashContext{Args: p.Ref}
		sc.LoginProviders = func() []tui.OAuthProvider {
			out := make([]tui.OAuthProvider, len(p.Providers))
			for i, pr := range p.Providers {
				out[i] = tui.OAuthProvider{ID: pr.ID, Name: pr.Name, AuthType: pr.AuthType}
			}
			return out
		}
		sc.StartProviderLogin = func(provider tui.OAuthProvider) error {
			got = append(got, []any{"start", provider.ID, provider.AuthType})
			return nil
		}
		sc.SelectAuthMethod = func(options []tui.OAuthProvider) (string, bool) {
			var list any
			if options != nil {
				ids := []string{}
				for _, o := range options {
					ids = append(ids, o.ID+"/"+o.AuthType)
				}
				list = ids
			}
			got = append(got, []any{"authType", list})
			return "", false
		}
		sc.SelectAuthProvider = func(_ string, _ []tui.OAuthProvider, search string) (tui.OAuthProvider, bool) {
			got = append(got, []any{"providers", nil, search})
			return tui.OAuthProvider{}, false
		}
		sc.Append = func(string) {}
		sc.ShowStatus = func(string) {}
		if err := handleLoginCommand(sc); err != nil {
			t.Fatal(err)
		}
		// The no-reference selector search is null in Pi; Pig passes it as ""; a null search and an empty one open the same selector.
		for _, step := range got {
			if step[0] == "providers" && step[2] == "" {
				step[2] = nil
			}
		}
		normalized := normalizeLoginRecord(want[i])
		if !reflect.DeepEqual(normalizeLoginRecord(toRecord(got)), normalized) {
			if failures++; failures <= 8 {
				t.Errorf("/login %q over %v: Pig %v, Pi %v", p.Ref, p.Providers, got, want[i])
			}
		}
		if len(want[i]) > 0 {
			outcomes[fmt.Sprint(want[i][0][0])]++
		}
	}
	if failures > 8 {
		t.Errorf("%d of %d probes differ", failures, len(probes))
	}
	for _, kind := range []string{"start", "authType", "providers"} {
		if outcomes[kind] < 50 {
			t.Errorf("only %d probes end in %s; the generator no longer exercises it", outcomes[kind], kind)
		}
	}
}

func toRecord(steps [][]any) [][]any { return steps }

// normalizeLoginRecord renders every step as text, so JSON numbers and string slices compare alike.
func normalizeLoginRecord(steps [][]any) []string {
	out := make([]string, len(steps))
	for i, step := range steps {
		parts := make([]string, len(step))
		for j, part := range step {
			switch typed := part.(type) {
			case []string:
				parts[j] = "[" + strings.Join(typed, ",") + "]"
			case []any:
				items := make([]string, len(typed))
				for k, item := range typed {
					items[k] = fmt.Sprint(item)
				}
				parts[j] = "[" + strings.Join(items, ",") + "]"
			default:
				parts[j] = fmt.Sprint(typed)
			}
		}
		out[i] = strings.Join(parts, "|")
	}
	return out
}
