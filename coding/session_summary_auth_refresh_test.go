package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi's CLI creates every Session through createAgentSessionRuntime -> createAgentSession, whose stream function resolves auth through the model runtime
// for every request; _getSummarizationRequestAuth (agent-session.ts:585-607) therefore pre-resolves nothing for it. pig's CLI creates its Session
// through Runtime.New, which used to skip that mark, so a compaction, branch summary or bug report pinned the credential resolved when it started
// into every request it makes: the second request of a split-turn or chunked summary, and every retry after a backoff, then sent a derived
// token (GitHub Copilot's lives about 30 minutes) that had expired meanwhile and got the provider's 401.

// summaryAuthWorld is a hermetic provider world on an injected clock: token endpoints that mint short-lived tokens, and a chat endpoint that accepts
// only a token whose lifetime has not passed on that clock.
type summaryAuthWorld struct {
	t      *testing.T
	mu     sync.Mutex
	offset time.Duration
	seq    int
	expiry map[string]time.Time
	// refreshStatus, when not 0, is the status every token endpoint answers.
	refreshStatus int
	tokenCalls    int
	// failNext makes the next chat requests answer a transient 503; onChat runs at the start of each chat request, numbered from 1 after arm.
	failNext int
	onChat   func(n int)
	chatSeen []string
	// server is the endpoint every chat request reaches; its host is the one a derived token names.
	server *httptest.Server
	host   string
	armed  int
	models []string
}

func newSummaryAuthWorld(t *testing.T) *summaryAuthWorld {
	t.Helper()
	world := &summaryAuthWorld{t: t, expiry: map[string]time.Time{"sk-static": time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)}}
	world.server = httptest.NewServer(http.HandlerFunc(world.serveChat))
	t.Cleanup(world.server.Close)
	world.host = world.server.Listener.Addr().String()
	previous := http.DefaultTransport
	http.DefaultTransport = world
	t.Cleanup(func() { http.DefaultTransport = previous })
	return world
}

func (w *summaryAuthWorld) now() time.Time { return time.Now().Add(w.offset) }

// mint registers a token that lives ttl on the world's clock and returns it with its expiry on the real clock the credential store compares against.
func (w *summaryAuthWorld) mint(format string, ttl time.Duration) (string, time.Time) {
	w.seq++
	token := fmt.Sprintf(format, w.seq)
	expires := w.now().Add(ttl)
	w.expiry[token] = expires
	return token, expires.Add(-w.offset)
}

// advance moves the injected clock: tokens minted before expire on it, and the stored credential's expiry shifts with it, as the real clock would have moved it.
func (w *summaryAuthWorld) advance(services *AgentSessionServices, providerID string, d time.Duration) {
	w.t.Helper()
	w.mu.Lock()
	w.offset += d
	w.mu.Unlock()
	if providerID == "" {
		return
	}
	if err := services.Auth().Update(providerID, func(current ai.Credential, exists bool) (ai.Credential, error) {
		if exists {
			current.SetExpiresMillis(current.ExpiresMillis() - float64(d.Milliseconds()))
		}
		return current, nil
	}); err != nil {
		w.t.Fatal(err)
	}
}

func (w *summaryAuthWorld) arm(failNext int, onChat func(n int)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.armed, w.failNext, w.onChat = len(w.chatSeen), failNext, onChat
}

// bearers are the Authorization headers the chat endpoint saw after arm, in order.
func (w *summaryAuthWorld) bearers() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.chatSeen[w.armed:]...)
}

func (w *summaryAuthWorld) refreshes() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.tokenCalls
}

func textResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)), Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func (w *summaryAuthWorld) RoundTrip(req *http.Request) (*http.Response, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	host, path := req.URL.Host, req.URL.Path
	switch {
	case host == "api.github.com" && path == "/copilot_internal/v2/token":
		w.tokenCalls++
		if w.refreshStatus != 0 {
			return textResponse(w.refreshStatus, `{"message":"Bad credentials"}`), nil
		}
		token, expires := w.mint("tid=%d;proxy-ep="+w.host+";", 30*time.Minute)
		return textResponse(200, fmt.Sprintf(`{"token":%q,"expires_at":%d}`, token, expires.Unix())), nil
	case host == w.host && path == "/models":
		entries := make([]string, len(w.models))
		for i, id := range w.models {
			entries[i] = fmt.Sprintf(`{"id":%q,"model_picker_enabled":true,"policy":{"state":"enabled"},"capabilities":{"supports":{"tool_calls":true}}}`, id)
		}
		return textResponse(200, `{"data":[`+strings.Join(entries, ",")+`]}`), nil
	case host == "platform.claude.com" && path == "/v1/oauth/token":
		w.tokenCalls++
		if w.refreshStatus != 0 {
			return textResponse(w.refreshStatus, `{"error":"invalid_grant"}`), nil
		}
		token, _ := w.mint("sk-ant-oat01-%d", 30*time.Minute)
		return textResponse(200, fmt.Sprintf(`{"access_token":%q,"refresh_token":"rotated-refresh","expires_in":1800}`, token)), nil
	case host == "oauth.fake.test" && path == "/token":
		w.tokenCalls++
		if w.refreshStatus != 0 {
			return textResponse(w.refreshStatus, `{"error":"invalid_grant"}`), nil
		}
		token, expires := w.mint("plain-access-%d", 30*time.Minute)
		return textResponse(200, fmt.Sprintf(`{"access_token":%q,"expires_at_ms":%d}`, token, expires.UnixMilli())), nil
	}
	return nil, fmt.Errorf("unexpected request %s %s", req.Method, req.URL)
}

func (w *summaryAuthWorld) serveChat(rw http.ResponseWriter, req *http.Request) {
	authorization := req.Header.Get("Authorization")
	w.mu.Lock()
	w.chatSeen = append(w.chatSeen, authorization)
	n, onChat := len(w.chatSeen)-w.armed, w.onChat
	w.mu.Unlock()
	if onChat != nil {
		onChat(n)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failNext > 0 {
		w.failNext--
		http.Error(rw, `{"error":{"message":"overloaded"}}`, http.StatusServiceUnavailable)
		return
	}
	expires, known := w.expiry[strings.TrimPrefix(strings.TrimPrefix(authorization, "Bearer "), "derived:")]
	if !known || !w.now().Before(expires) {
		http.Error(rw, `{"error":{"message":"Unauthorized"}}`, http.StatusUnauthorized)
		return
	}
	rw.Header().Set("Content-Type", "text/event-stream")
	if strings.HasSuffix(req.URL.Path, "/messages") {
		_, _ = io.WriteString(rw, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"m\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":3,\"output_tokens\":0}}}\n\n"+
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"+
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"summary text\"}}\n\n"+
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"+
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n"+
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		return
	}
	_, _ = io.WriteString(rw, "data: {\"choices\":[{\"delta\":{\"content\":\"summary text\"},\"finish_reason\":null}]}\n\n"+
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
}

// summaryAuthShape is one provider shape of the shared-path matrix.
type summaryAuthShape struct {
	name string
	// credentialProvider is the provider whose stored credential the world's clock shifts; empty for a shape with no expiring credential.
	credentialProvider string
	// expires reports whether the shape's request credential can expire.
	expires bool
	// offline marks a shape whose chat endpoint a test cannot reach (the derived base URL of GitHub Copilot is https-only and its provider has no insecure option), so it is checked at the pure layer only.
	offline bool
	// setup writes the agent directory and returns the model the CLI would resolve for it.
	setup func(t *testing.T, w *summaryAuthWorld, agentDir string) func(services *AgentSessionServices) *ai.Model
}

func writeAgentFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

const summaryAuthSettings = `{"cacheWarming":"off","retry":{"enabled":true,"maxRetries":1,"baseDelayMs":1,"provider":{"maxRetries":0}}}`

func summaryAuthShapes() []summaryAuthShape {
	return []summaryAuthShape{
		{
			// OAuth whose request token is derived and short-lived: the production github-copilot provider, resolved the way the CLI resolves --model.
			name: "derived-token-oauth-github-copilot", credentialProvider: "github-copilot", expires: true, offline: true,
			setup: func(t *testing.T, w *summaryAuthWorld, agentDir string) func(*AgentSessionServices) *ai.Model {
				token, expires := w.mint("tid=%d;proxy-ep="+w.host+";", 30*time.Minute)
				writeAgentFile(t, agentDir, "auth.json", fmt.Sprintf(`{"github-copilot":{"type":"oauth","access":%q,"refresh":"ghu_refresh","expires":%d}}`, token, expires.UnixMilli()-5*60*1000))
				return func(services *AgentSessionServices) *ai.Model {
					for _, candidate := range services.ModelRuntime().GetModels() {
						if candidate.ProviderMeta.ProviderID == "github-copilot" && candidate.ProviderMeta.API == ai.APIOpenAICompletions {
							w.models = []string{candidate.ID}
							model, err := BuildModel("github-copilot/"+candidate.ID, services)
							if err != nil {
								t.Fatal(err)
							}
							return model
						}
					}
					t.Fatal("catalog has no github-copilot openai-completions model")
					return nil
				}
			},
		},
		{
			// Plain OAuth with a refresh token: Anthropic's subscription login, a built-in provider the CLI builds through the registry, on a custom base URL.
			name: "plain-oauth-anthropic", credentialProvider: "anthropic", expires: true,
			setup: func(t *testing.T, w *summaryAuthWorld, agentDir string) func(*AgentSessionServices) *ai.Model {
				token, expires := w.mint("sk-ant-oat01-%d", 30*time.Minute)
				writeAgentFile(t, agentDir, "models.json", fmt.Sprintf(`{"providers":{"anthropic":{"baseUrl":%q}}}`, w.server.URL))
				writeAgentFile(t, agentDir, "auth.json", fmt.Sprintf(`{"anthropic":{"type":"oauth","access":%q,"refresh":"plain-refresh","expires":%d}}`, token, expires.UnixMilli()-5*60*1000))
				return func(services *AgentSessionServices) *ai.Model {
					candidates := services.ModelRuntime().GetModels("anthropic")
					if len(candidates) == 0 {
						t.Fatal("catalog has no anthropic model")
					}
					model, err := BuildModel("anthropic/"+candidates[0].ID, services)
					if err != nil {
						t.Fatal(err)
					}
					return model
				}
			},
		},
		{
			// OAuth whose request key is derived from the stored access token, registered by an extension with its own refresh endpoint.
			name: "derived-token-oauth-extension", credentialProvider: "fake-oauth", expires: true,
			setup: func(t *testing.T, w *summaryAuthWorld, agentDir string) func(*AgentSessionServices) *ai.Model {
				token, expires := w.mint("plain-access-%d", 30*time.Minute)
				writeAgentFile(t, agentDir, "auth.json", fmt.Sprintf(`{"fake-oauth":{"type":"oauth","access":%q,"refresh":"plain-refresh","expires":%d}}`, token, expires.UnixMilli()))
				return func(services *AgentSessionServices) *ai.Model {
					err := services.ModelRuntime().RegisterProvider("fake-oauth", ProviderConfigInput{Name: "Fake OAuth", BaseURL: w.server.URL + "/v1", API: ai.APIOpenAICompletions, Models: []ai.AnyModel{&ai.Model{ID: "m1", DisplayName: "m1", Input: []string{"text"}, Capabilities: ai.ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: 8192}}}, OAuth: &ExtensionOAuthConfig{
						Name: "Fake OAuth",
						RefreshToken: func(ctx context.Context, current ai.Credential) (ai.Credential, error) {
							request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth.fake.test/token", strings.NewReader(current.Refresh))
							if err != nil {
								return ai.Credential{}, err
							}
							response, err := http.DefaultClient.Do(request)
							if err != nil {
								return ai.Credential{}, err
							}
							defer func() { _ = response.Body.Close() }()
							if response.StatusCode != http.StatusOK {
								return ai.Credential{}, fmt.Errorf("token endpoint answered %d", response.StatusCode)
							}
							var body struct {
								Access    string `json:"access_token"`
								ExpiresAt int64  `json:"expires_at_ms"`
							}
							if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
								return ai.Credential{}, err
							}
							return ai.Credential{Type: ai.CredentialOAuth, Access: body.Access, Refresh: current.Refresh, Expires: body.ExpiresAt}, nil
						},
						GetAPIKey: func(credential ai.Credential) string { return "derived:" + credential.Access },
					}})
					if err != nil {
						t.Fatal(err)
					}
					model, err := BuildModel("fake-oauth/m1", services)
					if err != nil {
						t.Fatal(err)
					}
					return model
				}
			},
		},
		{
			// An API key on an OpenAI-compatible custom base URL: a registry-built provider with a configured key.
			name: "api-key-custom-base-url",
			setup: func(t *testing.T, w *summaryAuthWorld, agentDir string) func(*AgentSessionServices) *ai.Model {
				writeAgentFile(t, agentDir, "models.json", fmt.Sprintf(`{"providers":{"custom-gw":{"baseUrl":%q,"apiKey":"sk-static","api":"openai-completions","models":[{"id":"m1"}]}}}`, w.server.URL+"/v1"))
				return func(services *AgentSessionServices) *ai.Model {
					model, err := BuildModel("custom-gw/m1", services)
					if err != nil {
						t.Fatal(err)
					}
					return model
				}
			},
		},
	}
}

// summaryAuthSession builds the Session the CLI builds: a runtime's New over a model BuildModel resolved, then one successful turn.
func summaryAuthSession(t *testing.T, shape summaryAuthShape, turn bool) (*summaryAuthWorld, *AgentSessionServices, *Session) {
	t.Helper()
	world := newSummaryAuthWorld(t)
	agentDir := t.TempDir()
	writeAgentFile(t, agentDir, "settings.json", summaryAuthSettings)
	resolve := shape.setup(t, world, agentDir)
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	model := resolve(services)
	runtime, err := NewRuntime(RuntimeOptions{Services: services})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	session, err := runtime.New(SessionStartOptions{Model: model, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	services.SettingsManager().ApplyOverrides(icodingagent.Settings{Compaction: &icodingagent.CompactionSettingsJSON{KeepRecentTokens: new(1.)}})
	if !turn {
		return world, services, session
	}
	if err := session.Prompt(t.Context(), "first question"); err != nil {
		t.Fatal(err)
	}
	if err := session.WaitForIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	var last *agent.AssistantMessage
	for _, message := range session.Messages() {
		if message.Assistant != nil {
			last = message.Assistant
		}
	}
	if bearers := world.bearers(); len(bearers) == 0 || last == nil || last.StopReason != ai.StopReasonStop {
		t.Fatalf("the turn did not succeed: requests %q, last assistant %+v", bearers, last)
	}
	return world, services, session
}

// The pure layer: the request auth a summary resolves for the Session the CLI builds. Pi pre-resolves nothing for it (agent-session.ts:603 runtimeStreamFunctions
// check), so the stream resolves the credential of every request when it sends it, as a turn's does.
func TestSummarizationRequestResolvesNoCredentialForARuntimeSession(t *testing.T) {
	for _, shape := range summaryAuthShapes() {
		t.Run(shape.name, func(t *testing.T) {
			_, _, session := summaryAuthSession(t, shape, false)
			request, err := session.prepareSummarizationRequest(t.Context(), session.Model())
			if err != nil {
				t.Fatal(err)
			}
			if request.apiKey != "" || len(request.headers) != 0 || len(request.env) != 0 {
				t.Fatalf("the summary request carries credentials resolved before it streams: apiKey=%q headers=%v env=%v", request.apiKey, request.headers, request.env)
			}
		})
	}
}

// summaryOperation is one non-turn model request path that resolves its request auth through prepareSummarizationRequest.
type summaryOperation struct {
	name string
	run  func(t *testing.T, session *Session) error
}

func summaryOperations() []summaryOperation {
	return []summaryOperation{
		{"compaction", func(t *testing.T, session *Session) error {
			result, err := session.Compact(t.Context(), "")
			if err == nil && !strings.Contains(result.Summary, "summary text") {
				err = fmt.Errorf("summary %q", result.Summary)
			}
			return err
		}},
		{"branch-summary", func(t *testing.T, session *Session) error {
			var target string
			for _, entry := range session.inner.GetEntries() {
				if entry.Base().Type == "message" {
					target = entry.Base().ID
					break
				}
			}
			result, err := session.NavigateTree(t.Context(), target, NavigateTreeOptions{Summarize: true})
			if err == nil && (result.SummaryEntry == nil || !strings.Contains(result.SummaryEntry.Summary, "summary text")) {
				err = fmt.Errorf("navigation %+v", result)
			}
			return err
		}},
		{"bug-report", func(t *testing.T, session *Session) error {
			text, err := session.SummarizeForBugReport(t.Context(), "it broke")
			if err == nil && !strings.Contains(text, "summary text") {
				err = fmt.Errorf("bug report summary %q", text)
			}
			return err
		}},
	}
}

// The nearest caller: a summary that takes more than one attempt (a transient failure, then the retry after its backoff) while the credential expires, after a
// turn that succeeded with the same Session. The retry must carry a credential resolved when it is sent.
func TestSummaryAttemptsEachSendAFreshCredentialAfterExpiry(t *testing.T) {
	for _, shape := range summaryAuthShapes() {
		if shape.offline {
			continue
		}
		for _, operation := range summaryOperations() {
			t.Run(shape.name+"/"+operation.name, func(t *testing.T) {
				world, services, session := summaryAuthSession(t, shape, true)
				turnBearer := world.bearers()[0]
				world.arm(1, func(n int) {
					if n == 1 {
						world.advance(services, shape.credentialProvider, 31*time.Minute)
					}
				})
				if err := operation.run(t, session); err != nil {
					t.Fatalf("%s: %v; seen %q", operation.name, err, world.bearers())
				}
				bearers := world.bearers()
				if len(bearers) != 2 {
					t.Fatalf("the summary sent %d requests, want the failed one and its retry: %q", len(bearers), bearers)
				}
				if shape.expires && bearers[1] == bearers[0] {
					t.Fatalf("the retry after expiry sent the expired credential %q again", bearers[1])
				}
				if shape.expires && bearers[0] != turnBearer {
					t.Fatalf("the first summary request sent %q, the turn sent %q: the credential had not expired yet", bearers[0], turnBearer)
				}
			})
		}
	}
}

// Fresh credentials and credentials that expired before the summary started are both resolved by the stream, as a turn's are.
func TestCompactionResolvesCredentialWhenItStartsFreshAndExpired(t *testing.T) {
	for _, shape := range summaryAuthShapes() {
		for _, expired := range []bool{false, true} {
			if shape.offline {
				continue
			}
			t.Run(fmt.Sprintf("%s/expired=%t", shape.name, expired), func(t *testing.T) {
				world, services, session := summaryAuthSession(t, shape, true)
				if expired {
					world.advance(services, shape.credentialProvider, 31*time.Minute)
				}
				world.arm(0, nil)
				result, err := session.Compact(t.Context(), "")
				if err != nil || !strings.Contains(result.Summary, "summary text") {
					t.Fatalf("compaction = %+v, %v; seen %q", result, err, world.bearers())
				}
				if bearers := world.bearers(); len(bearers) != 1 {
					t.Fatalf("the summary sent %d requests: %q", len(bearers), bearers)
				}
			})
		}
	}
}

// Hermetic failure: the refresh the summary needs is refused. The error surfaces once, as Pi's refresh failure does for a turn, no request carries a dead
// credential, and automatic compaction leaves the Session consistent: no compaction entry, one end event.
func TestAutoCompactionSurfacesRefreshFailureOnce(t *testing.T) {
	for _, shape := range summaryAuthShapes() {
		if !shape.expires || shape.offline {
			continue
		}
		t.Run(shape.name, func(t *testing.T) {
			world, services, session := summaryAuthSession(t, shape, true)
			world.advance(services, shape.credentialProvider, 31*time.Minute)
			world.mu.Lock()
			world.refreshStatus = http.StatusUnauthorized
			world.mu.Unlock()
			world.arm(0, nil)
			var mu sync.Mutex
			var ends []agent.CompactionEndEvent
			session.Subscribe(func(event agent.AgentEvent) {
				if end, ok := event.(agent.CompactionEndEvent); ok {
					mu.Lock()
					ends = append(ends, end)
					mu.Unlock()
				}
			})
			compacted, err := session.runAutoCompaction(t.Context(), "threshold", false)
			if compacted || err != nil {
				t.Fatalf("runAutoCompaction = %v, %v", compacted, err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(ends) != 1 || !strings.HasPrefix(ends[0].ErrorMessage, "Auto-compaction failed: ") || !strings.Contains(ends[0].ErrorMessage, "summarization failed: OAuth refresh failed for "+shape.credentialProvider) {
				t.Fatalf("end events = %+v", ends)
			}
			if bearers := world.bearers(); len(bearers) != 0 {
				t.Fatalf("a request was sent although its credential could not be resolved: %q", bearers)
			}
			if got := len(suiteCompactions(t, session)); got != 0 {
				t.Fatalf("the failed compaction left %d compaction entries", got)
			}
		})
	}
}
