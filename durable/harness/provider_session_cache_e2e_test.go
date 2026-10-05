// Ports packages/durable/test/provider-session-cache-e2e.test.ts.

package harness

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// providerSessionCacheModels is the test's Proxy over builtinModels(): StreamSimple records the forwarded provider session ID and streams the model through openai-codex-responses with the token over SSE. baseURL replaces the model's endpoint for the hermetic run.
type providerSessionCacheModels struct {
	*ai.Models
	token   string
	baseURL string

	mu        sync.Mutex
	forwarded []string
}

func (models *providerSessionCacheModels) StreamSimple(ctx context.Context, model *ai.Model, request ai.Context, options ...ai.StreamOptions) *ai.AssistantMessageEventStream {
	var option ai.StreamOptions
	if len(options) > 0 {
		option = options[0]
	}
	models.mu.Lock()
	models.forwarded = append(models.forwarded, option.SessionID)
	models.mu.Unlock()
	target := new(*model)
	if models.baseURL != "" {
		target.ProviderMeta.BaseURL = models.baseURL
	}
	option.APIKey = models.token
	option.Transport = ai.TransportSSE
	// A setup failure ends the stream with an error message, as streamSimple's does.
	return ai.LazyStream(ctx, target, func(ctx context.Context) (*ai.AssistantMessageEventStream, error) {
		return ai.StreamSimple(ctx, target, ai.NormalizeContext(request), option)
	})
}

func (models *providerSessionCacheModels) forwardedCopy() []string {
	models.mu.Lock()
	defer models.mu.Unlock()
	return slices.Clone(models.forwarded)
}

// cacheProbe is the first turn's intentionally repeated context (provider-session-cache-e2e.test.ts:19-30).
func cacheProbe(nonce string) string {
	lines := []string{
		"This is a real automated prompt-cache test. The repeated context below is intentional.",
		"Read it silently, then reply with exactly: CACHE PROBE READY",
	}
	for index := range 180 {
		lines = append(lines, fmt.Sprintf("%s immutable cache record %03d: alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau upsilon phi chi psi omega.", nonce, index))
	}
	return strings.Join(lines, "\n")
}

func cacheHitRate(usage ai.Usage) float64 {
	promptTokens := usage.Input + usage.CacheRead + usage.CacheWrite
	if promptTokens == 0 {
		return 0
	}
	return float64(usage.CacheRead) / float64(promptTokens)
}

// runProviderSessionCacheCase is "reuses the Codex prompt cache across Durable turns" (provider-session-cache-e2e.test.ts:48-121).
func runProviderSessionCacheCase(t *testing.T, token, baseURL string) {
	t.Helper()
	models := &providerSessionCacheModels{Models: ai.BuiltinModels(), token: token, baseURL: baseURL}
	harness, err := OpenHarness(testContext, storage.NewMemoryStorage(), HarnessOptions{
		Models:   models,
		Registry: CreateRegistry(),
		Settings: func() *HarnessSettings {
			return &HarnessSettings{
				Stream:     &durable.ConversationStreamOptions{Transport: ai.TransportSSE},
				Compaction: &CompactionPolicyPatch{Enabled: new(false)},
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeHarness(t, harness)
	root, err := harness.Root(testContext, &RootOptions{Agent: &AgentChange{Model: SetTo(durable.ModelRef{Provider: "openai-codex", ModelId: "gpt-5.5"})}})
	if err != nil {
		t.Fatal(err)
	}
	harness.Resume()
	usages := []ai.Usage{}
	firstHit := -1
	for turn := range 8 {
		content := fmt.Sprintf("Reply exactly: CACHE PROBE FOLLOW-UP %d", turn)
		if turn == 0 {
			nonce, err := ai.UUIDv7(nil)
			if err != nil {
				t.Fatal(err)
			}
			content = cacheProbe(nonce)
		}
		settled := must(submitInput(t, root, content).Wait(testContext))
		if settled.Status != durable.SubmissionDone || settled.Type != durable.SubmissionTypeInput {
			t.Fatalf("Unexpected submission status: %s", settled.Status)
		}
		answer := commitValue(t, root, func(tx durable.Tx) (*durable.TypedEntry[durable.Never], error) {
			return durable.TxEntry(tx, durable.AssistantEntry, *settled.Answer)
		})
		if answer.ConversationId != root.Id() {
			t.Fatal("Answer belongs to another conversation")
		}
		message, ok := answer.Model[0].(ai.AssistantMessage)
		if !ok {
			t.Fatalf("answer = %#v", answer.Model[0])
		}
		if message.StopReason == ai.StopReasonError {
			t.Fatalf("stopReason error: %s", message.ErrorMessage)
		}
		usages = append(usages, message.Usage)
		if firstHit == -1 && message.Usage.CacheRead > 0 {
			firstHit = len(usages) - 1
		}
		if firstHit != -1 && len(usages)-firstHit >= 3 {
			break
		}
	}
	sessionId := providerSessionId(t, harness, root.Id())
	want := make([]string, len(usages))
	for index := range want {
		want[index] = sessionId
	}
	if forwarded := models.forwardedCopy(); !slices.Equal(forwarded, want) {
		t.Fatalf("forwarded session IDs = %q, want %q", forwarded, want)
	}
	if firstPrompt := usages[0].Input + usages[0].CacheRead + usages[0].CacheWrite; firstPrompt < 4_000 {
		t.Fatalf("first prompt = %d tokens, want at least 4000", firstPrompt)
	}
	if usages[0].CacheRead != 0 {
		t.Fatalf("first cacheRead = %d, want 0", usages[0].CacheRead)
	}
	if firstHit != 1 {
		t.Fatalf("first cache hit at turn %d, want 1", firstHit)
	}
	cached := usages[firstHit:]
	if len(cached) != 3 {
		t.Fatalf("cached turns = %d, want 3", len(cached))
	}
	for _, usage := range cached {
		if rate := cacheHitRate(usage); rate < 0.9 {
			t.Fatalf("cache hit rate = %f for %+v, want at least 0.9", rate, usage)
		}
	}
}

// codexPromptCacheEndpoint is a Codex Responses endpoint whose prompt cache is keyed by prompt_cache_key and the session-id header, as the Codex backend's is: a request whose key matches an earlier request reads that request's prompt as a cached prefix.
func codexPromptCacheEndpoint(t *testing.T) string {
	t.Helper()
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(decoder.Close)
	var mu sync.Mutex
	cachedPrompt := map[string][]byte{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err == nil && r.Header.Get("Content-Encoding") == "zstd" {
			raw, err = decoder.DecodeAll(raw, nil)
		}
		var payload map[string]any
		if err == nil {
			err = json.Unmarshal(raw, &payload)
		}
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		key, _ := payload["prompt_cache_key"].(string)
		if key != r.Header.Get("session-id") {
			t.Errorf("prompt_cache_key %q and session-id %q differ", key, r.Header.Get("session-id"))
		}
		prompt, err := json.Marshal(map[string]any{"instructions": payload["instructions"], "input": payload["input"]})
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Four bytes per token is the conventional estimate; the cached prefix is the earlier prompt with the same key.
		promptTokens := len(prompt) / 4
		cachedTokens := 0
		mu.Lock()
		if previous, ok := cachedPrompt[key]; ok && key != "" {
			for cachedTokens < len(previous) && cachedTokens < len(prompt) && previous[cachedTokens] == prompt[cachedTokens] {
				cachedTokens++
			}
			cachedTokens /= 4
		}
		if key != "" {
			cachedPrompt[key] = prompt
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		item := `{"type":"message","id":"msg_reply","role":"assistant","status":"completed","content":[{"type":"output_text","text":"CACHE PROBE READY","annotations":[]}]}`
		_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg_reply\",\"role\":\"assistant\",\"content\":[]}}\n\n")
		_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":%s}\n\n", item)
		_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":%d,\"input_tokens_details\":{\"cached_tokens\":%d},\"output_tokens\":4,\"total_tokens\":%d}}}\n\n", promptTokens, cachedTokens, promptTokens+4)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// codexHermeticToken is a Codex access token whose JWT claims carry a ChatGPT account ID, as Codex requests require.
func codexHermeticToken() string {
	claims, _ := json.Marshal(map[string]any{"https://api.openai.com/auth": map[string]string{"chatgpt_account_id": "acct_durable"}})
	return "header." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"
}

// The upstream case runs against the live Codex backend (provider_session_cache_e2e_live_test.go). This hermetic run sends the same Durable turns through the real Codex provider to an endpoint whose cache is keyed by the forwarded identity, so the persisted provider session ID decides every hit.
func TestProviderSessionCacheE2EHermetic(t *testing.T) {
	// upstream: packages/durable/test/provider-session-cache-e2e.test.ts:48
	runProviderSessionCacheCase(t, codexHermeticToken(), codexPromptCacheEndpoint(t))
}
