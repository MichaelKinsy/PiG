package codingagent

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

const parityHarnessSamplePngBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aK9sAAAAASUVORK5CYII="

func parityHarnessEnabled() bool {
	return os.Getenv("PIG_PARITY_HARNESS") == "1"
}

func (m *InteractiveMode) probeClipboardRead() (string, error) {
	bytes, mime, err := readClipboardProbe(m.opts.AgentDir)
	if err != nil {
		return "", err
	}
	if len(bytes) == 0 || mime == "" {
		return "clipboard:none", nil
	}
	return "clipboard:" + mime + ":" + strconv.Itoa(len(bytes)), nil
}

func readClipboardProbe(agentDir string) ([]byte, string, error) {
	var (
		bytes []byte
		mime  string
	)
	withParityFixturePath(agentDir, func() {
		if data, kind, result := tryWlPaste(); result == clipboardImageFound {
			bytes, mime = data, kind
			return
		}
		if data, kind, result := tryXclip(); result == clipboardImageFound {
			bytes, mime = data, kind
		}
	})
	return bytes, mime, nil
}

func withParityFixturePath(agentDir string, fn func()) {
	fixtureBin := filepath.Join(agentDir, "bin")
	if _, err := os.Stat(fixtureBin); err != nil {
		fn()
		return
	}
	oldPath := os.Getenv("PATH")
	_ = os.Setenv("PATH", fixtureBin+string(os.PathListSeparator)+oldPath)
	defer func() { _ = os.Setenv("PATH", oldPath) }()
	fn()
}

func (m *InteractiveMode) probeImageFallback() (string, error) {
	img := tui.NewImage(parityHarnessSamplePngBase64, "image/png", tui.ImageOptions{Filename: "probe.png"}, nil)
	for _, line := range img.Render(80) {
		if strings.TrimSpace(line) != "" {
			return line, nil
		}
	}
	return "", nil
}

func (m *InteractiveMode) probeCancellableLoader() (string, error) {
	loader := tui.NewCancellableLoader("", "", "Working...", nil)
	aborted := false
	loader.OnAbort = func() { aborted = true }
	rendered := strings.Join(loader.Render(40), "\n")
	renderOK := strings.Contains(rendered, "Working...")
	loader.HandleInput("\x1b")
	ctxAborted := loader.Aborted()
	loader.Dispose()
	return "cancellable-loader:render=" + boolString(renderOK) + ":aborted=" + boolString(ctxAborted) + ":onAbort=" + boolString(aborted), nil
}

func (m *InteractiveMode) probeSelectList() (string, error) {
	list := tui.NewFilterableList("", []string{"a", "b", "c"})
	list.EnableSearch = false
	list.SetCursor(2)
	list.HandleInput("\x1b[6~")
	pageIgnored := list.CursorIndex() == 2
	list.HandleInput("\x1b[B")
	wrapped := ""
	if list.CursorIndex() == 0 {
		wrapped = "a"
	}
	list.HandleInput("\r")
	selected := ""
	if list.SelectedIndex() == 0 {
		selected = "a"
	}
	cancelList := tui.NewFilterableList("", []string{"a"})
	cancelList.EnableSearch = false
	cancelList.HandleInput("\x1b")
	rendered := strings.Contains(strings.Join(list.Render(40), "\n"), "a")
	return fmt.Sprintf("select-list:render=%t:pageIgnored=%t:wrap=%s:select=%s:cancel=%t",
		rendered, pageIgnored, wrapped, selected, cancelList.Cancelled()), nil
}

func (m *InteractiveMode) probeOAuthShared() (string, error) {
	providers := ai.GetOAuthProviders()
	ids := make([]string, 0, len(providers))
	for _, p := range providers {
		ids = append(ids, oauthProviderAlias(p.ID()))
	}
	sort.Strings(ids)

	ai.RegisterOAuthProvider("probe-custom", parityOAuthProvider{id: "probe-custom", name: "Probe Custom"})
	defer ai.UnregisterOAuthProvider("probe-custom")
	creds, apiKey, err := ai.GetOAuthAPIKey("probe-custom", map[string]ai.OAuthCredentials{
		"probe-custom": {Refresh: "probe-refresh", Access: "stale", Expires: 1},
	})
	if err != nil {
		return "", err
	}
	pkce, err := ai.GeneratePKCE()
	if err != nil {
		return "", err
	}
	urlsafe := oauthURLSafe(pkce.Verifier) && oauthURLSafe(pkce.Challenge)
	return fmt.Sprintf("oauth-shared:ids=%s:custom=%s:api=%s:exp=%t:pkce=%t",
		strings.Join(ids, ","), providerID(ai.GetOAuthProvider("probe-custom")), apiKey, creds != nil && creds.Expires > 1,
		len(pkce.Verifier) == 43 && len(pkce.Challenge) == 43 && urlsafe), nil
}

func (m *InteractiveMode) probeOAuthCallbackPage() (string, error) {
	success := ai.OAuthSuccessHTML("done <ok>")
	errorPage := ai.OAuthErrorHTML("bad <tag>", "line1\nline2 & more")
	return fmt.Sprintf("oauth-page:success=%t:error=%t:escaped=%t:details=%t",
		strings.Contains(success, "Authentication successful") && strings.Contains(success, "done &lt;ok&gt;"),
		strings.Contains(errorPage, "Authentication failed") && strings.Contains(errorPage, "bad &lt;tag&gt;"),
		!strings.Contains(errorPage, "<tag>") && strings.Contains(errorPage, "&amp; more"),
		strings.Contains(errorPage, "line1") && strings.Contains(errorPage, "line2")), nil
}

func (m *InteractiveMode) probeCopilotHeaders() (string, error) {
	messages := []ai.Message{
		ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "done"}}},
		ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: "caption"}, ai.ImageContent{MimeType: "image/png", Data: parityHarnessSamplePngBase64}}},
	}
	initiator := inferCopilotInitiatorProbe(messages)
	vision := hasCopilotVisionProbe(messages)
	headers := map[string]string{"X-Initiator": initiator, "Openai-Intent": "conversation-edits"}
	if vision {
		headers["Copilot-Vision-Request"] = "true"
	}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lowerKeys := make([]string, 0, len(keys))
	for _, k := range keys {
		lowerKeys = append(lowerKeys, strings.ToLower(k))
	}
	return fmt.Sprintf("copilot-headers:init=%s:intent=%s:vision=%s:keys=%s",
		strings.ToLower(headers["X-Initiator"]), strings.ToLower(headers["Openai-Intent"]), strings.ToLower(headers["Copilot-Vision-Request"]), strings.Join(lowerKeys, ",")), nil
}

func (m *InteractiveMode) probeOAuthCopilot() (string, error) {
	var (
		mu          sync.Mutex
		polls       int
		policyCalls int
	)
	cred, err := withMockDefaultClient(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case req.URL.Host == "github.com" && req.URL.Path == "/login/device/code":
			return jsonResponse(200, `{"device_code":"device-123","user_code":"ABCD-EFGH","verification_uri":"https://github.com/login/device","interval":0,"expires_in":60}`), nil
		case req.URL.Host == "github.com" && req.URL.Path == "/login/oauth/access_token":
			polls++
			if polls == 1 {
				return jsonResponse(200, `{"error":"authorization_pending"}`), nil
			}
			return jsonResponse(200, `{"access_token":"ghu_refresh"}`), nil
		case req.URL.Host == "api.github.com" && req.URL.Path == "/copilot_internal/v2/token":
			return jsonResponse(200, `{"token":"tid=x;proxy-ep=proxy.individual.githubcopilot.com;other=y","expires_at":4102444800}`), nil
		case req.URL.Host == "api.individual.githubcopilot.com" && req.URL.Path == "/models":
			return jsonResponse(200, `{"data":[{"id":"gpt-4o","model_picker_enabled":true,"policy":{"state":"enabled"},"capabilities":{"supports":{"tool_calls":true}}}]}`), nil
		case req.URL.Host == "api.individual.githubcopilot.com" && strings.HasPrefix(req.URL.Path, "/models/") && strings.HasSuffix(req.URL.Path, "/policy"):
			policyCalls++
			return jsonResponse(200, `{"ok":true}`), nil
		default:
			return nil, fmt.Errorf("unexpected copilot request: %s %s", req.Method, req.URL.String())
		}
	}, func() (ai.Credential, error) {
		return ai.LoginGitHubCopilot(context.Background(), ai.CopilotLoginCallbacks{
			OnPrompt:   func(context.Context) (string, error) { return "", nil },
			OnAuth:     func(string, string) {},
			OnProgress: func(string) {},
		})
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("copilot-oauth:refresh=%s:base=%s:polls=%d:policy=%t:exp=%t",
		cred.Refresh, copilotBaseURLProbe(cred.Access, cred.EnterpriseDomain), polls, policyCalls > 0, cred.Expires > time.Now().UnixMilli()), nil
}

func loginGitHubCopilotForParity(ctx context.Context, cb ai.CopilotLoginCallbacks) (ai.Credential, error) {
	if os.Getenv("PIG_PARITY_LOGIN_DIALOG") != "1" {
		return ai.LoginGitHubCopilot(ctx, cb)
	}
	return withMockDefaultClient(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Host == "github.com" && req.URL.Path == "/login/device/code":
			return jsonResponse(200, `{"device_code":"dialog-device","user_code":"ABCD-EFGH","verification_uri":"https://github.com/login/device","interval":30,"expires_in":60}`), nil
		case req.URL.Host == "github.com" && req.URL.Path == "/login/oauth/access_token":
			return jsonResponse(200, `{"error":"authorization_pending"}`), nil
		default:
			return nil, fmt.Errorf("unexpected login-dialog request: %s %s", req.Method, req.URL.String())
		}
	}, func() (ai.Credential, error) {
		return ai.LoginGitHubCopilot(ctx, cb)
	})
}

// probeOAuthCopilotEnv exercises the github-copilot env-fallback path: an auth
// store with no stored credential plus an env API key. Mirrors upstream
// auth-storage.getApiKey step 4 (env token used directly as the bearer) and
// getGitHubCopilotBaseUrl (base URL from the token's proxy-ep). Before the env
// fallback existed, this returned "not logged in".
func (m *InteractiveMode) probeOAuthCopilotEnv() (string, error) {
	dir, err := os.MkdirTemp("", "copilot-env-probe")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	auth, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		return "", err
	}
	const envToken = "tid=parity;proxy-ep=proxy.individual.githubcopilot.com;exp=4102444800"
	bearer, base, err := ai.ResolveCopilotEnvFallback(auth, envToken)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("copilot-env:match=%t:base=%s", bearer == envToken, base), nil
}

func (m *InteractiveMode) probeOAuthAnthropic() (string, error) {
	var gotBody string
	creds, err := withMockDefaultClient(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "platform.claude.com" && req.URL.Path == "/v1/oauth/token" {
			body, _ := io.ReadAll(req.Body)
			gotBody = string(body)
			return jsonResponse(200, `{"access_token":"anth-access","refresh_token":"anth-refresh","expires_in":3600}`), nil
		}
		return nil, fmt.Errorf("unexpected anthropic request: %s %s", req.Method, req.URL.String())
	}, func() (ai.OAuthCredentials, error) {
		return ai.LoginAnthropic(context.Background(), ai.OAuthLoginCallbacks{
			OnAuth:            func(ai.OAuthAuthInfo) {},
			OnManualCodeInput: func() (string, error) { return "anth-code", nil },
			OnProgress:        func(string) {},
		})
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("anthropic-oauth:name=%s:refresh=%s:access=%s:grant=%t:pkce=%t:exp=%t",
		ai.AnthropicOAuthDisplayName,
		creds.Refresh, creds.Access,
		strings.Contains(gotBody, `"grant_type":"authorization_code"`),
		strings.Contains(gotBody, `"code_verifier":"`),
		creds.Expires > time.Now().UnixMilli()), nil
}

func (m *InteractiveMode) probeOAuthCodex() (string, error) {
	var gotForm string
	creds, err := withMockDefaultClient(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "auth.openai.com" && req.URL.Path == "/oauth/token" {
			body, _ := io.ReadAll(req.Body)
			gotForm = string(body)
			jwt := makeCodexJWT("acct_123")
			return jsonResponse(200, fmt.Sprintf(`{"access_token":%q,"refresh_token":"codex-refresh","expires_in":3600}`, jwt)), nil
		}
		return nil, fmt.Errorf("unexpected codex request: %s %s", req.Method, req.URL.String())
	}, func() (ai.OAuthCredentials, error) {
		return ai.LoginOpenAICodex(context.Background(), ai.OAuthLoginCallbacks{
			OnAuth:            func(ai.OAuthAuthInfo) {},
			OnManualCodeInput: func() (string, error) { return "codex-code", nil },
			OnProgress:        func(string) {},
		})
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("codex-oauth:name=%s:refresh=%s:account=%s:grant=%t:pkce=%t:exp=%t",
		ai.OpenAICodexOAuthDisplayName,
		creds.Refresh, ai.CodexAccountID(creds.Access),
		strings.Contains(gotForm, "grant_type=authorization_code"),
		strings.Contains(gotForm, "code_verifier="),
		creds.Expires > time.Now().UnixMilli()), nil
}

// probeOAuthOpenRouter drives the OpenRouter PKCE flow through its manual-code
// path against a stubbed token endpoint, proving the key exchange carries the
// PKCE verifier + S256 method and yields a permanent (non-expiring) key. Not a
// method: it needs nothing from InteractiveMode.
func probeOAuthOpenRouter() (string, error) {
	var gotBody string
	creds, err := withMockDefaultClient(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() == "https://openrouter.ai/api/v1/auth/keys" {
			body, _ := io.ReadAll(req.Body)
			gotBody = string(body)
			return jsonResponse(200, `{"key":"or-key"}`), nil
		}
		return nil, fmt.Errorf("unexpected openrouter request: %s %s", req.Method, req.URL.String())
	}, func() (ai.OAuthCredentials, error) {
		return ai.LoginOpenRouter(context.Background(), ai.OAuthLoginCallbacks{
			OnAuth:            func(ai.OAuthAuthInfo) {},
			OnManualCodeInput: func() (string, error) { return "or-code", nil },
			OnProgress:        func(string) {},
		})
	})
	if err != nil {
		return "", err
	}
	refresh := creds.Refresh
	if refresh == "" {
		refresh = "none"
	}
	return fmt.Sprintf("openrouter-oauth:access=%s:refresh=%s:pkce=%t:s256=%t:exp=%t",
		creds.Access, refresh,
		strings.Contains(gotBody, `"code_verifier":"`),
		strings.Contains(gotBody, `"code_challenge_method":"S256"`),
		creds.Expires > time.Now().UnixMilli()), nil
}

// probeOAuthXai drives the xAI device-code flow against stubbed device and
// token endpoints, proving the device grant type and credential expiry. The xAI
// provider uses an injected client (nil Transport), so the transport-level mock
// is required. Not a method: it needs nothing from InteractiveMode.
func probeOAuthXai() (string, error) {
	provider, ok := ai.GetOAuthProvider("xai")
	if !ok {
		return "", fmt.Errorf("xai oauth provider not registered")
	}
	var gotForm string
	creds, err := withMockDefaultTransport(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Host == "auth.x.ai" && req.URL.Path == "/oauth2/device/code":
			return jsonResponse(200, `{"device_code":"dc","user_code":"UCODE","verification_uri":"https://x.ai/device","expires_in":600,"interval":1}`), nil
		case req.URL.Host == "auth.x.ai" && req.URL.Path == "/oauth2/token":
			body, _ := io.ReadAll(req.Body)
			gotForm = string(body)
			return jsonResponse(200, `{"access_token":"xai-access","refresh_token":"xai-refresh","expires_in":3600}`), nil
		}
		return nil, fmt.Errorf("unexpected xai request: %s %s", req.Method, req.URL.String())
	}, func() (ai.OAuthCredentials, error) {
		return provider.Login(ai.OAuthLoginCallbacks{
			OnDeviceCode: func(ai.OAuthDeviceCodeInfo) {},
			OnProgress:   func(string) {},
		})
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("xai-oauth:access=%s:refresh=%s:grant=%t:exp=%t",
		creds.Access, creds.Refresh,
		strings.Contains(gotForm, "grant_type=urn"),
		creds.Expires > time.Now().UnixMilli()), nil
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

type parityOAuthProvider struct {
	id   string
	name string
}

func (p parityOAuthProvider) ID() string               { return p.id }
func (p parityOAuthProvider) Name() string             { return p.name }
func (p parityOAuthProvider) UsesCallbackServer() bool { return false }
func (p parityOAuthProvider) Login(ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	return ai.OAuthCredentials{}, fmt.Errorf("unused in parity harness")
}
func (p parityOAuthProvider) RefreshToken(creds ai.OAuthCredentials) (ai.OAuthCredentials, error) {
	creds.Access = "probe-access"
	creds.Expires = time.Now().Add(time.Hour).UnixMilli()
	return creds, nil
}
func (p parityOAuthProvider) GetAPIKey(creds ai.OAuthCredentials) string { return creds.Access }

func providerID(provider ai.OAuthProviderInterface, ok bool) string {
	if !ok || provider == nil {
		return ""
	}
	return provider.ID()
}

func oauthProviderAlias(id string) string {
	switch id {
	case "github-copilot":
		return "copilot"
	case "openai-codex":
		return "codex"
	default:
		return id
	}
}

func oauthURLSafe(s string) bool {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return s != ""
}

func inferCopilotInitiatorProbe(msgs []ai.Message) string {
	if len(msgs) == 0 {
		return "user"
	}
	if _, ok := msgs[len(msgs)-1].(ai.UserMessage); !ok {
		return "agent"
	}
	return "user"
}

func hasCopilotVisionProbe(msgs []ai.Message) bool {
	for _, message := range msgs {
		var blocks []ai.ContentBlock
		switch value := message.(type) {
		case ai.UserMessage:
			if content, ok := value.Content.(ai.UserContentBlocks); ok {
				blocks = make([]ai.ContentBlock, len(content))
				for i, block := range content {
					blocks[i] = block
				}
			}
		case ai.ToolResultMessage:
			blocks = make([]ai.ContentBlock, len(value.Content))
			for i, block := range value.Content {
				blocks[i] = block
			}
		}
		for _, block := range blocks {
			if _, ok := block.(ai.ImageContent); ok {
				return true
			}
		}
	}
	return false
}

func copilotBaseURLProbe(accessToken, enterpriseDomain string) string {
	for part := range strings.SplitSeq(accessToken, ";") {
		if v, ok := strings.CutPrefix(part, "proxy-ep="); ok && strings.TrimSpace(v) != "" {
			return "https://api." + strings.TrimPrefix(strings.TrimSpace(v), "proxy.")
		}
	}
	if enterpriseDomain != "" {
		return "https://copilot-api." + enterpriseDomain
	}
	return "https://api.individual.githubcopilot.com"
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func withMockDefaultClient[T any](handler func(*http.Request) (*http.Response, error), fn func() (T, error)) (T, error) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripperFunc(handler)}
	defer func() { http.DefaultClient = oldClient }()
	return fn()
}

// withMockDefaultTransport swaps http.DefaultTransport, which intercepts both
// http.DefaultClient and any *http.Client with a nil Transport (the injected
// clients used by the device-code providers such as xAI and Kimi).
func withMockDefaultTransport[T any](handler func(*http.Request) (*http.Response, error), fn func() (T, error)) (T, error) {
	old := http.DefaultTransport
	http.DefaultTransport = roundTripperFunc(handler)
	defer func() { http.DefaultTransport = old }()
	return fn()
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func makeCodexJWT(accountID string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, `{"https://api.openai.com/auth":{"chatgpt_account_id":%q}}`, accountID))
	return header + "." + payload + ".sig"
}
