//go:build !pig_strip_bedrock_converse_stream

package ai

// pi: packages/ai/src/api/bedrock-converse-stream.ts

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type bedrockMatchProbe struct {
	ID         string  `json:"id"`
	Name       *string `json:"name"`
	ForceCache bool    `json:"forceCache"`
}

type bedrockMatchResult struct {
	Candidates []string `json:"candidates"`
	Adaptive   bool     `json:"adaptive"`
	Xhigh      bool     `json:"xhigh"`
	Binding    bool     `json:"binding"`
	Claude     bool     `json:"claude"`
	Caching    bool     `json:"caching"`
}

// bedrockMatchProbes combine model ids (base models, regional inference profiles, ARNs, application inference profile ARNs with no model name) with display names written with runs of separators, tabs, no-break spaces,
// upper case, dotted versions and non-Claude vendors, plus empty and absent names.
func bedrockMatchProbes() []bedrockMatchProbe {
	random := rand.New(rand.NewSource(20260951))
	ids := []string{"anthropic.claude-3-7-sonnet-20250219-v1:0", "us.anthropic.claude-opus-4-6-v1", "eu.anthropic.claude-sonnet-4-5", "anthropic.claude-3-5-haiku-20241022-v1:0", "global.anthropic.claude-fable-5", "us.anthropic.claude-opus-5",
		"arn:aws:bedrock:us-east-1:123456789012:application-inference-profile/abc123", "arn:aws-us-gov:bedrock:us-gov-west-1::foundation-model/anthropic.claude-sonnet-4-5", "my-claude-profile", "claude", "CLAUDE-OPUS-4-7", "amazon.nova-pro-v1:0",
		"openai.gpt-oss-120b-1:0", "openai.gpt-5", "meta.llama3-70b", "", " ", " claude-3-7-sonnet ", "\tanthropic.claude-sonnet-4", "anthropic/claude-opus-4.8", "anthropic.claude--opus--4--6", "opus_4_7", "SONNET.5", "haiku:5", "FABLE 5", "İstanbul-claude-3-7-sonnet"}
	names := []string{"", "Claude 3.7 Sonnet", "Claude  3.7  Sonnet", "Claude Opus 4.6", "claude\t3.7\tsonnet", "claude\u00a03.7\u00a0sonnet", "claude\u20033.7", "CLAUDE-OPUS-4-7", "Opus 4.7 (EU)", "Sonnet 4.6 - prod", "My Profile", "my_claude.profile:v2",
		"Anthropic.Claude", "anthropic/claude", "GPT-OSS 120B", "GPT 5", " ", " claude ", "claude\u0085sonnet\u00854", "claude\ufeffopus\ufeff5", "FABLE_5", "haiku  5", "Haiku::5", "sonnet...5", "opus___4___6"}
	var probes []bedrockMatchProbe
	for range 4500 {
		probe := bedrockMatchProbe{ID: ids[random.Intn(len(ids))], ForceCache: random.Intn(3) == 0}
		if choice := random.Intn(len(names) + 3); choice < len(names) {
			name := names[choice]
			probe.Name = &name
		}
		probes = append(probes, probe)
	}
	return probes
}

// The Bedrock model-matching helpers, cut from Pi's source, run in Node on the same ids and names: the lowercased candidate list with its separator-run rewrite, the adaptive-thinking, native-xhigh and block-binding
// families, the Claude check (which tests the id for the anthropic prefixes only and the name for claude) and the prompt-caching decision with AWS_BEDROCK_FORCE_CACHE must match Pig's helpers.
func TestBedrockModelMatchingMatchesPi(t *testing.T) {
	probes := bedrockMatchProbes()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "ai", "src", "api", "bedrock-converse-stream.ts")
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/bedrock_model_match.mjs", source)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []bedrockMatchResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Pi answered %d of %d probes", len(expected), len(probes))
	}
	differing, claude, caching, adaptive := 0, 0, 0, 0
	for i, probe := range probes {
		name := ""
		if probe.Name != nil {
			name = *probe.Name
		}
		env := ProviderEnv{}
		if probe.ForceCache {
			env = ProviderEnv{"AWS_BEDROCK_FORCE_CACHE": "1"}
		}
		got := bedrockMatchResult{
			Candidates: bedrockModelMatchCandidates(probe.ID, name),
			Adaptive:   supportsBedrockAdaptiveThinkingWithName(probe.ID, name),
			Xhigh:      supportsNativeXhighEffort(&Model{ID: probe.ID, DisplayName: name}),
			Binding:    supportsBedrockThinkingBlockBinding(probe.ID, name),
			Claude:     isAnthropicClaudeBedrockModel(probe.ID, name),
			Caching:    supportsBedrockPromptCaching(probe.ID, name, env),
		}
		want := expected[i]
		if want.Claude {
			claude++
		}
		if want.Caching {
			caching++
		}
		if want.Adaptive {
			adaptive++
		}
		if !reflect.DeepEqual(got, want) {
			if differing++; differing <= 12 {
				t.Errorf("%+v name=%q\n  Pig %+v\n  Pi  %+v", probe, name, got, want)
			}
		}
	}
	if differing > 12 {
		t.Errorf("%d of %d probes differ from Pi", differing, len(probes))
	}
	if claude < 500 || claude > len(probes)-500 || caching < 300 || adaptive < 300 {
		t.Errorf("probes are one-sided: %d claude, %d caching, %d adaptive", claude, caching, adaptive)
	}
}
