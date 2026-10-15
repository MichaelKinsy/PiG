package ai_test

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/ai/src/env-api-keys.ts getEnvApiKey, findEnvKeys

// The API key a provider reads from its environment (the variable names of every provider, Anthropic's token precedence, Vertex ADC and project and
// location, the AWS credential sources of Bedrock, Copilot's token) for random environments of set, empty and blank values, against the pinned pi-ai.
func TestEnvAPIKeysMatchPi(t *testing.T) {
	providers := []string{"anthropic", "github-copilot", "ant-ling", "qwen-token-plan", "qwen-token-plan-cn", "qwen-token-plan-individual", "openai", "azure", "nvidia", "deepseek", "google", "google-vertex", "groq", "cerebras", "xai", "typesafe", "radius", "openrouter", "vercel-ai-gateway", "zai", "zai-coding-cn", "mistral", "minimax", "minimax-cn", "moonshotai", "moonshotai-cn", "huggingface", "fireworks", "together", "baseten", "opencode", "opencode-go", "kimi-coding", "meta", "cloudflare-workers-ai", "cloudflare-ai-gateway", "xiaomi", "xiaomi-token-plan-cn", "xiaomi-token-plan-ams", "xiaomi-token-plan-sgp", "amazon-bedrock", "openai-codex", "unknown", ""}
	names := []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "COPILOT_GITHUB_TOKEN", "ANT_LING_API_KEY", "QWEN_TOKEN_PLAN_API_KEY", "QWEN_TOKEN_PLAN_CN_API_KEY", "OPENAI_API_KEY", "AZURE_OPENAI_API_KEY", "NVIDIA_API_KEY", "DEEPSEEK_API_KEY", "GEMINI_API_KEY", "GOOGLE_CLOUD_API_KEY", "GROQ_API_KEY", "CEREBRAS_API_KEY", "XAI_API_KEY", "TYPESAFE_API_KEY", "RADIUS_API_KEY", "OPENROUTER_API_KEY", "AI_GATEWAY_API_KEY", "ZAI_API_KEY", "ZAI_CODING_CN_API_KEY", "MISTRAL_API_KEY", "MINIMAX_API_KEY", "MINIMAX_CN_API_KEY", "MOONSHOT_API_KEY", "HF_TOKEN", "FIREWORKS_API_KEY", "TOGETHER_API_KEY", "BASETEN_API_KEY", "OPENCODE_API_KEY", "KIMI_API_KEY", "META_API_KEY", "CLOUDFLARE_API_KEY", "XIAOMI_API_KEY", "XIAOMI_TOKEN_PLAN_CN_API_KEY", "XIAOMI_TOKEN_PLAN_AMS_API_KEY", "XIAOMI_TOKEN_PLAN_SGP_API_KEY",
		"GOOGLE_APPLICATION_CREDENTIALS", "GOOGLE_CLOUD_PROJECT", "GCLOUD_PROJECT", "GOOGLE_CLOUD_LOCATION", "AWS_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_BEARER_TOKEN_BEDROCK", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_WEB_IDENTITY_TOKEN_FILE"}
	// The process environment is one of the sources, so the names are cleared in the test process (and so in Pi's).
	for _, name := range names {
		t.Setenv(name, "")
	}
	existing := filepath.Join(t.TempDir(), "adc.json")
	if err := os.WriteFile(existing, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(191))
	type probe struct {
		Provider string            `json:"provider"`
		Env      map[string]string `json:"env"`
	}
	var probes []probe
	for range 3000 {
		env := map[string]string{}
		for range rng.Intn(14) {
			name := names[rng.Intn(len(names))]
			env[name] = []string{"v", "", " ", "x y", "tok"}[rng.Intn(5)]
		}
		provider := providers[rng.Intn(len(providers))]
		if provider == "google-vertex" || rng.Intn(8) == 0 {
			// The default ADC file is probed once per process, so Vertex probes name their credentials file.
			env["GOOGLE_APPLICATION_CREDENTIALS"] = []string{existing, filepath.Join(filepath.Dir(existing), "missing.json")}[rng.Intn(2)]
			if rng.Intn(2) == 0 {
				env["GOOGLE_CLOUD_PROJECT"], env["GOOGLE_CLOUD_LOCATION"] = "p", "l"
			}
		}
		probes = append(probes, probe{provider, env})
	}
	input, _ := json.Marshal(probes)
	cmd := exec.CommandContext(t.Context(), "node", "testdata/env_api_keys.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []struct {
		Key   *string  `json:"key"`
		Found []string `json:"found"`
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	failures, keyed := 0, 0
	for i, p := range probes {
		key, found := ai.GetEnvAPIKey(p.Provider, p.Env), ai.FindEnvKeys(p.Provider, p.Env)
		wantKey := ""
		if want[i].Key != nil {
			wantKey = *want[i].Key
			keyed++
		}
		if key != wantKey || !slices.Equal(found, want[i].Found) {
			if failures++; failures <= 6 {
				t.Errorf("%s %v:\n PiG %q %v\n Pi  %q %v", p.Provider, p.Env, key, found, wantKey, want[i].Found)
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
	if keyed < 250 {
		t.Fatalf("Pi found a key in only %d probes", keyed)
	}
}
