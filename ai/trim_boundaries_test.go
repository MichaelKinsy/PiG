package ai

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// Expectations follow ECMAScript String.prototype.trim, not Go Unicode White_Space.
var ecmaTrimCases = []struct{ name, input, want string }{
	{"empty", "", ""},
	{"ASCII", " \t\r\n", ""},
	{"NBSP", "\u00a0", ""},
	{"BOM", " \ufeff\t", ""},
	{"NEL", " \u0085 ", "\u0085"},
	{"ordinary", " \ufeffvalue\u00a0 ", "value"},
	{"NEL edges", "\u0085value\u0085", "\u0085value\u0085"},
	{"stress", "\ufeff" + strings.Repeat("x", 8192) + "\ufeff", strings.Repeat("x", 8192)},
}

func TestGrammarDefinitionsUseECMAScriptTrim(t *testing.T) {
	// packages/ai/src/api/constrained-sampling.ts:245-265: trim only decides presence; the definition itself is unchanged.
	for _, tc := range ecmaTrimCases {
		for _, format := range []string{GrammarFormatOpenAILark, GrammarFormatOpenAIRegex} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				tool := ToolSchema{Name: "calc", Parameters: grammarSchema(), ConstrainedSampling: &ConstrainedSamplingConfig{Type: "grammar", Variants: map[string]string{format: tc.input}}}
				grammar, err := resolveGrammarConstrainedSampling(tool, true)
				blank := tc.want == ""
				if (err != nil) != blank || (!blank && (grammar == nil || grammar.Definition != tc.input)) {
					t.Fatalf("grammar=%+v err=%v, blank=%v", grammar, err, blank)
				}
				completions, err := grammarTrueProvider().convertTools([]ToolSchema{tool})
				if (err != nil) != blank || (!blank && completions[0].Custom.Format.Grammar.Definition != tc.input) {
					t.Fatalf("completions=%+v err=%v", completions, err)
				}
				responses, err := (&openAIResponsesProvider{}).convertTools([]ToolSchema{tool}, false, true)
				if (err != nil) != blank || (!blank && responses[0].Format.Definition != tc.input) {
					t.Fatalf("responses=%+v err=%v", responses, err)
				}
			})
		}
	}
}

func TestDirectSimpleHeadersUseECMAScriptTrim(t *testing.T) {
	// hasHeader in anthropic-messages.ts:300-303, openai-completions.ts:75-78 and openai-responses.ts:37-40.
	for _, api := range []API{APIAnthropicMessages, APIOpenAICompletions, APIOpenAIResponses} {
		for _, tc := range ecmaTrimCases {
			t.Run(string(api)+"/"+tc.name, func(t *testing.T) {
				model := &Model{ID: "custom", ProviderMeta: ProviderMetadata{API: api, ProviderID: "custom", BaseURL: "https://example.invalid/v1"}}
				called := false
				stop := errors.New("stop before network")
				stream, err := StreamSimple(t.Context(), model, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{
					Headers:   ProviderHeaders{"Authorization": new(tc.input)},
					OnPayload: func(any, *Model) (any, error) { called = true; return nil, stop },
				})
				if stream != nil {
					result := stream.Result()
					if result.ErrorMessage != stop.Error() {
						t.Fatalf("stream error=%q", result.ErrorMessage)
					}
				}
				if called != (tc.want != "") {
					t.Fatalf("payload called=%v err=%v", called, err)
				}
				if !called && (err == nil || err.Error() != "No API key for provider: custom") {
					t.Fatalf("missing-auth error=%v", err)
				}
			})
		}
	}
}

func TestAnthropicBetaFeaturesUseECMAScriptTrim(t *testing.T) {
	// anthropic-messages.ts:1004-1013 trims each comma-separated feature before filtering and Set deduplication.
	for _, tc := range ecmaTrimCases {
		t.Run(tc.name, func(t *testing.T) {
			headers := ProviderHeaders{"anthropic-beta": new(tc.input + ", stable,stable," + tc.input)}
			want := []string{"stable"}
			if tc.want != "" {
				want = []string{tc.want, "stable"}
			}
			got := getBetaFeatures(anthropicHeaders{}, anthropicHeadersFromProviderHeaders(headers), anthropicBetaInputs{})
			if !slices.Equal(got, want) {
				t.Fatalf("features=%q want=%q", got, want)
			}
			clearAnthropicAuthEnv(t)
			captured, _ := runAnthropicWire(t, AnthropicConfig{APIKey: "key"}, anthropicAuthContext, StreamOptions{Headers: headers}, endTurn)
			if got := captured.header.Get("anthropic-beta"); got != strings.Join(want, ",") {
				t.Fatalf("wire beta=%q want=%q", got, strings.Join(want, ","))
			}
		})
	}
}

func TestOAuthPastedInputUsesECMAScriptTrim(t *testing.T) {
	// auth/oauth/{anthropic,openai-codex,openrouter}.ts:53,74,53 trim the pasted value before URL/query/bare-code parsing.
	for _, tc := range ecmaTrimCases {
		t.Run(tc.name, func(t *testing.T) {
			for name, parse := range map[string]func(string) (string, string){"anthropic": parseAuthorizationInput, "codex": parseCodexAuthorizationInput} {
				if code, state := parse(tc.input); code != tc.want || state != "" {
					t.Errorf("%s code=%q state=%q want=%q", name, code, state, tc.want)
				}
			}
			if code := parseOpenRouterAuthorizationInput(tc.input); code != tc.want {
				t.Errorf("openrouter code=%q want=%q", code, tc.want)
			}
		})
	}
	// BOM around a redirect must be removed before the URL parser chooses the query branch.
	redirect := "\ufeffhttp://localhost/callback?code=abc&state=xyz\ufeff"
	for _, parse := range []func(string) (string, string){parseAuthorizationInput, parseCodexAuthorizationInput} {
		if code, state := parse(redirect); code != "abc" || state != "xyz" {
			t.Errorf("redirect code=%q state=%q", code, state)
		}
	}
	if got := parseOpenRouterAuthorizationInput(redirect); got != "abc" {
		t.Errorf("openrouter redirect=%q", got)
	}
	// github-copilot.ts:42 trims the enterprise domain before URL parsing.
	if domain, ok := normalizeDomain("\ufeffcompany.ghe.com\ufeff"); !ok || domain != "company.ghe.com" {
		t.Errorf("enterprise domain=%q ok=%v", domain, ok)
	}
}

func TestAuthEnvironmentUsesECMAScriptTrim(t *testing.T) {
	// auth/context.ts:27 uses trim only for presence: credentials retain all nonblank bytes.
	const key = "PIG_TEST_UNICODE_AUTH"
	for _, tc := range ecmaTrimCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(key, tc.input)
			ctx := DefaultProviderAuthContext()
			got, ok := ctx.Env(key)
			if ok != (tc.want != "") || (ok && got != tc.input) {
				t.Fatalf("env=%q present=%v", got, ok)
			}
			result, err := ResolveProviderAuth(t.Context(), "custom", ProviderAuth{APIKey: EnvAPIKeyAuth("key", key)}, NewInMemoryAuthStorage(nil), ctx, AuthResolutionOverrides{})
			if err != nil || (result != nil) != ok || (ok && result.Auth.APIKey != tc.input) {
				t.Fatalf("auth=%+v err=%v", result, err)
			}
		})
	}
}

func TestErrorDetailsUseECMAScriptTrim(t *testing.T) {
	// auth/resolve.ts:39; auth/oauth/meta.ts:52; providers/radius-config.ts:76.
	for _, tc := range ecmaTrimCases {
		t.Run(tc.name, func(t *testing.T) {
			cause := errors.New(tc.input)
			want := "failed"
			if tc.want != "" {
				want += ": " + tc.want
			}
			got := NewModelsError(ModelsErrorAuth, "failed", cause)
			if got.Error() != want || !errors.Is(got, cause) {
				t.Errorf("ModelsError=%q want=%q", got.Error(), want)
			}
			body := map[string]any{"error_description": tc.input, "detail": "fallback"}
			detail := ": fallback"
			if tc.want != "" {
				detail = ": " + tc.want
			}
			if got := metaErrorDetail(body); got != detail {
				t.Errorf("Meta detail=%q want=%q", got, detail)
			}
			provider := testMetaProvider(func(*http.Request) (*http.Response, error) { return metaJSONResponse(400, body), nil })
			_, err := provider.LoginContext(t.Context(), OAuthLoginCallbacks{})
			if err == nil || err.Error() != "Meta device authorization failed with status 400"+detail {
				t.Errorf("Meta login error=%v", err)
			}
			// The gateway caller must include the correctly trimmed and capped body in its failure.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(400); _, _ = fmt.Fprint(w, tc.input) }))
			defer server.Close()
			_, err = LoadRadiusGatewayConfig(t.Context(), server.URL, "")
			radiusBody := tc.want
			if len(radiusBody) > 512 {
				radiusBody = radiusBody[:512] + "…" // The stress case contains only ASCII after trimming.
			}
			if err == nil || err.Error() != "Could not load Radius config from "+server.URL+": 400: "+radiusBody {
				t.Errorf("Radius error=%v", err)
			}
		})
	}
}

func TestCodexIntervalUsesECMAScriptTrim(t *testing.T) {
	// auth/oauth/openai-codex.ts:217 applies Number(interval.trim()).
	for _, tc := range []struct {
		input string
		want  float64
		valid bool
	}{{"\ufeff", 0, true}, {" \ufeff5\ufeff ", 5, true}, {"\u0085", 0, false}, {"\u00855\u0085", 0, false}} {
		if got, ok := codexParseInterval(tc.input); got != tc.want || ok != tc.valid {
			t.Errorf("interval %q = %v,%v want %v,%v", tc.input, got, ok, tc.want, tc.valid)
		}
	}
}
