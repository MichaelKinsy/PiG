package extensionconformance

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/sdkscenarios"
)

// stepTimeout bounds one host-side step. A step that waits on an SDK that never
// answers fails its row instead of hanging the suite.
const stepTimeout = 60 * time.Second

// runStep runs one scenario step against the rig and appends what it observed.
func (r *scenarioRig) runStep(step sdkscenarios.Step, lines *[]string) {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(r.t.Context(), stepTimeout)
	defer cancel()
	switch step.Op {
	case "settle":
		r.services.ModelRuntime().Refresh(ctx, ai.ModelsRefreshOptions{AllowNetwork: new(false)})
	case "methods":
		r.observeMethods(step.Provider, lines)
	case "runtime-methods":
		r.observeRuntimeMethods(step.Provider, lines)
	case "models":
		var ids []string
		for _, model := range r.services.ModelRuntime().GetModels(step.Provider) {
			ids = append(ids, model.ID)
		}
		scenariof(lines, "models %s %s", step.Provider, strings.Join(ids, ","))
	case "check":
		r.observeCheck(ctx, step.Provider, lines)
	case "login":
		r.observeLogin(ctx, step, lines)
	case "authjson":
		scenariof(lines, "auth.json %s %s", step.Provider, r.authJSON(step.Provider, step.Raw))
	case "setcredential":
		var credential ai.Credential
		if err := json.Unmarshal([]byte(step.Credential), &credential); err != nil {
			r.t.Fatalf("setcredential %s: %v", step.Provider, err)
		}
		if err := r.services.Auth().Set(step.Provider, credential); err != nil {
			r.t.Fatal(err)
		}
	case "resolve":
		r.observeResolve(ctx, step.Provider, lines)
	case "host-provider":
		native := r.services.Registry().NativeProvider(step.Provider)
		if native == nil {
			scenariof(lines, "host holds %s <none>", step.Provider)
		} else {
			scenariof(lines, "host holds %s %s", step.Provider, native.Name)
		}
	case "load-peer":
		r.loadPeer(lines)
	case "command":
		r.runCommand(ctx, step, lines)
	case "reload":
		r.reload(step, lines)
	case "end-input":
		r.endInput(ctx, step, lines)
	case "abort-tool":
		r.abortTool(ctx, step, lines)
	case "registrations":
		r.observeRegistrations(lines)
	case "tool":
		r.runTool(ctx, step, lines)
	case "tool-call-event":
		r.runToolCallEvent(ctx, step, lines)
	case "footer-state":
		r.runFooterState(step, lines)
	case "tool-result-event":
		r.runToolResultEvent(ctx, step, lines)
	case "input-event":
		r.runInputEvent(ctx, step, lines)
	case "run-signal":
		r.runSignal(step)
	case "abort-run-in-command":
		r.abortRunInCommand(ctx, step, lines)
	case "emit-event":
		r.emitEvent(ctx, step, lines)
	case "before-agent-start-event":
		r.runBeforeAgentStartEvent(ctx, step, lines)
	case "tool-result":
		r.runToolResult(ctx, step, lines)
	case "shortcut":
		r.runShortcut(ctx, step, lines)
	case "host-calls":
		r.observeHostCalls(ctx, step, lines)
	default:
		r.t.Fatalf("unknown scenario step op %q", step.Op)
	}
}

// observeMethods records the auth methods the host holds for a registered
// provider object: what the extension's SDK declared over the wire, composed by
// the host carrier (Pi model-runtime.ts registerNativeProvider keeps the object).
func (r *scenarioRig) observeMethods(id string, lines *[]string) {
	native := r.services.Registry().NativeProvider(id)
	if native == nil {
		scenariof(lines, "methods %s <no provider object>", id)
		return
	}
	scenariof(lines, "%s", describeMethods("methods", id, native.Auth))
}

// observeRuntimeMethods records the auth methods of the composed provider the
// model runtime returns for the ID (Pi model-runtime.ts getProvider).
func (r *scenarioRig) observeRuntimeMethods(id string, lines *[]string) {
	provider := r.services.ModelRuntime().GetProvider(id)
	if provider == nil {
		scenariof(lines, "runtime-methods %s <no provider>", id)
		return
	}
	scenariof(lines, "%s", describeMethods("runtime-methods", id, provider.Auth))
}

func describeMethods(prefix, id string, auth ai.ProviderAuth) string {
	apiKey, oauth := "none", "none"
	if method := auth.APIKey; method != nil {
		apiKey = fmt.Sprintf("%q login=%t", method.Name, method.Login != nil)
	}
	if method := auth.OAuth; method != nil {
		oauth = fmt.Sprintf("%q label=%q subscription=%t", method.Name, method.LoginLabel, method.IsSubscription)
	}
	return fmt.Sprintf("%s %s apiKey=%s oauth=%s", prefix, id, apiKey, oauth)
}

// observeLogin runs the runtime's login with scripted answers and records every
// prompt shown, every event notified and the outcome.
func (r *scenarioRig) observeLogin(ctx context.Context, step sdkscenarios.Step, lines *[]string) {
	answers := append([]string(nil), step.Answers...)
	interaction := ai.AuthInteraction{
		Prompt: func(_ context.Context, prompt ai.AuthPrompt) (string, error) {
			body, err := json.Marshal(prompt)
			if err != nil {
				return "", err
			}
			scenariof(lines, "prompt %s", body)
			if len(answers) == 0 {
				return "", errors.New("scenario has no answer for this prompt")
			}
			answer := answers[0]
			answers = answers[1:]
			if answer == "<cancel>" {
				return "", errors.New("Login cancelled")
			}
			return answer, nil
		},
		Notify: func(event ai.AuthEvent) {
			body, err := json.Marshal(event)
			if err != nil {
				body = []byte(err.Error())
			}
			scenariof(lines, "event %s", body)
		},
	}
	var options ai.LoginOptions
	if step.DeviceID != "" {
		var calls atomic.Int32
		options.GetDeviceID = func() string {
			calls.Add(1)
			return step.DeviceID
		}
		defer func() { scenariof(lines, "getDeviceId calls=%d", calls.Load()) }()
	}
	credential, err := r.loginThroughCarrier(ctx, step, interaction, options)
	if err != nil {
		if step.ErrorContains != "" && strings.Contains(err.Error(), step.ErrorContains) {
			scenariof(lines, "login %s %s error containing %q", step.Provider, step.Kind, step.ErrorContains)
			return
		}
		scenariof(lines, "login %s %s error: %s", step.Provider, step.Kind, firstLine(err.Error()))
		return
	}
	body, err := json.Marshal(credential)
	if err != nil {
		r.t.Fatal(err)
	}
	scenariof(lines, "login %s %s credential %s", step.Provider, step.Kind, canonicalJSON(r.t, body))
}

// observeCheck asks the provider object's own availability check, which the host
// carrier answers by calling the extension's auth.apiKey.check.
func (r *scenarioRig) observeCheck(ctx context.Context, id string, lines *[]string) {
	native := r.services.Registry().NativeProvider(id)
	if native == nil || native.CheckAuth == nil {
		scenariof(lines, "check %s <no check>", id)
		return
	}
	check, err := native.CheckAuth(ctx, nil)
	switch {
	case err != nil:
		scenariof(lines, "check %s error: %s", id, firstLine(err.Error()))
	case check == nil:
		scenariof(lines, "check %s <none>", id)
	default:
		scenariof(lines, "check %s type=%s source=%q", id, check.Type, check.Source)
	}
}

// observeResolve resolves the provider's auth as a model request does: through
// the provider object's own auth.apiKey.resolve, which refreshes an expired OAuth
// credential through its auth.oauth.refresh and converts it with auth.oauth.toAuth
// and commits the rotated credential.
func (r *scenarioRig) observeResolve(ctx context.Context, id string, lines *[]string) {
	result, err := r.services.Registry().NativeProviderAuth(ctx, id, ai.AuthResolutionOverrides{})
	switch {
	case err != nil:
		scenariof(lines, "resolve %s error: %s", id, firstLine(err.Error()))
	case result == nil:
		scenariof(lines, "resolve %s <none>", id)
	default:
		scenariof(lines, "resolve %s apiKey=%q source=%q", id, result.Auth.APIKey, result.Source)
	}
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

// loadCause is the extension's own failure message: the text after the host's
// connect prefix, up to the host's process note.
func loadCause(err error) string {
	text := firstLine(err.Error())
	if _, after, ok := strings.Cut(text, "process_exited: "); ok {
		text = after
	}
	cause, _, _ := strings.Cut(text, " (packed")
	return cause
}

// observe runs every step of the scenario and returns the observation.
func (r *scenarioRig) observe(scenario sdkscenarios.Scenario) []string {
	r.t.Helper()
	var lines []string
	if len(r.loadFailure) != 0 {
		cause := loadCause(r.loadFailure[0])
		if want := scenario.LoadErrorContains; want != "" && strings.Contains(cause, want) {
			return []string{"<the extension failed to load: contains " + strconv.Quote(want) + ">"}
		}
		return []string{"<the extension failed to load: " + cause + ">"}
	}
	for _, step := range scenario.Steps {
		r.runStep(step, &lines)
	}
	return lines
}

// loginThroughCarrier runs the provider object's own login for the method kind
// through the host carrier, which calls the extension's SDK over the wire, and
// persists the credential it returned to auth.json as the login dialog does
// (interactive_api_key_login.go credentials.Modify).
func (r *scenarioRig) loginThroughCarrier(ctx context.Context, step sdkscenarios.Step, interaction ai.AuthInteraction, options ai.LoginOptions) (ai.Credential, error) {
	native := r.services.Registry().NativeProvider(step.Provider)
	if native == nil {
		return ai.Credential{}, errors.New("no provider object registered")
	}
	var credential ai.Credential
	var err error
	switch ai.AuthType(step.Kind) {
	case ai.CredentialOAuth:
		if native.Auth.OAuth == nil || native.Auth.OAuth.Login == nil {
			return ai.Credential{}, errors.New("the provider object has no oauth login")
		}
		credential, err = native.Auth.OAuth.Login(ctx, interaction, options)
	case ai.CredentialAPIKey:
		if native.Auth.APIKey == nil || native.Auth.APIKey.Login == nil {
			return ai.Credential{}, errors.New("the provider object has no api_key login")
		}
		credential, err = native.Auth.APIKey.Login(ctx, interaction)
	default:
		return ai.Credential{}, fmt.Errorf("unknown login kind %q", step.Kind)
	}
	if err != nil {
		return ai.Credential{}, err
	}
	_, err = r.services.Auth().Modify(ctx, step.Provider, func(*ai.Credential) (*ai.Credential, error) { return &credential, nil })
	return credential, err
}

// loadPeer loads the scenario's second extension after the first, as another
// process registering later.
//
// The host binds an extension's lifetime to the context its Load or Reload
// received, so those calls take the test's context, not the step's.
func (r *scenarioRig) loadPeer(lines *[]string) {
	config := peerConfig(r.t, r.root)
	r.configs = append(r.configs, config)
	loaded, err := r.host.Load(r.t.Context(), config)
	if err != nil {
		r.t.Fatalf("load the peer extension: %v", err)
	}
	r.loaded = append(r.loaded, *loaded)
	scenariof(lines, "peer loaded")
}

// runCommand runs a command of the primary extension through the host with a
// result file as its argument and records the file.
func (r *scenarioRig) runCommand(ctx context.Context, step sdkscenarios.Step, lines *[]string) {
	// The registry reader includes a command registered after load.
	ext := r.primary()
	command, ok := ext.Command(step.Name)
	if !ok {
		scenariof(lines, "command %s <not registered>", step.Name)
		return
	}
	path := filepath.Join(r.dir, "result-"+step.Name)
	// A step with Args runs the command with them as its arguments and records what the echo op wrote.
	args := path
	if step.Args != "" {
		args = step.Args
		path = filepath.Join(r.dir, ".echo-args")
		_ = os.Remove(path)
	}
	if err := command.Handler(ctx, args); err != nil {
		if step.ErrorContains != "" && strings.Contains(err.Error(), step.ErrorContains) {
			scenariof(lines, "command %s error contains %q", step.Name, step.ErrorContains)
			return
		}
		scenariof(lines, "command %s error: %s", step.Name, firstLine(err.Error()))
		return
	}
	body, err := os.ReadFile(path)
	if err != nil {
		scenariof(lines, "command %s wrote nothing", step.Name)
		return
	}
	if step.JSON {
		body = []byte(canonicalJSON(r.t, body))
	}
	if step.Args != "" {
		if len(body) > longTextBytes {
			scenariof(lines, "command %s args %d bytes sha256 %x", step.Name, len(body), sha256.Sum256(body))
		} else {
			scenariof(lines, "command %s args %q", step.Name, body)
		}
		return
	}
	scenariof(lines, "command %s %s", step.Name, body)
}

// observeHostCalls waits until the extension's fire-and-forget host actions
// reached the host and records them in arrival order.
func (r *scenarioRig) observeHostCalls(ctx context.Context, step sdkscenarios.Step, lines *[]string) {
	r.calls.wait(ctx, step.Count)
	if got := len(r.calls.snapshot()); got < step.Count {
		scenariof(lines, "host calls: %d of %d arrived", got, step.Count)
		return
	}
	for _, call := range r.calls.take() {
		scenariof(lines, "host call %s", call)
	}
}

// observeRegistrations records what the host holds of the primary extension's
// tools, commands, shortcuts and flags, in registration order.
func (r *scenarioRig) observeRegistrations(lines *[]string) {
	ext := r.primary()
	for _, name := range ext.ToolOrder {
		tool := ext.Tools[name].Definition
		scenariof(lines, "tool %s %q params=%s", name, tool.Description, canonicalJSON(r.t, tool.Parameters))
	}
	// The registered readers include a registration made after load.
	for _, command := range ext.RegisteredCommands() {
		scenariof(lines, "command %s %q", command.Name, command.Description)
	}
	shortcuts := ext.RegisteredShortcuts()
	slices.SortFunc(shortcuts, func(a, b extension.ExtensionShortcut) int {
		return strings.Compare(string(a.Shortcut), string(b.Shortcut))
	})
	for _, shortcut := range shortcuts {
		scenariof(lines, "shortcut %s %q", shortcut.Shortcut, shortcut.Description)
	}
	for _, flag := range ext.RegisteredFlags() {
		def, err := json.Marshal(flag.Default)
		if err != nil {
			r.t.Fatal(err)
		}
		scenariof(lines, "flag %s type=%s default=%s %q", flag.Name, flag.Type, def, flag.Description)
	}
	messages, entries := ext.RegisteredRenderers()
	for _, customType := range slices.Sorted(maps.Keys(messages)) {
		scenariof(lines, "message renderer %s", customType)
	}
	for _, customType := range slices.Sorted(maps.Keys(entries)) {
		scenariof(lines, "entry renderer %s", customType)
	}
}

// runner is an extension runner over the extensions the host holds.
func (r *scenarioRig) runner() *inproc.Runner {
	return inproc.NewRunner(r.loaded, r.dir)
}

// runTool dispatches one call of a tool through the agent's production path
// (prepare, validate, execute) and records the tool result the model would see.
// longTextBytes is the size above which a tool step records a digest of the result instead of its text.
const longTextBytes = 1024

func (r *scenarioRig) runTool(ctx context.Context, step sdkscenarios.Step, lines *[]string) {
	var args map[string]any
	if err := json.Unmarshal([]byte(step.Args), &args); err != nil {
		r.t.Fatalf("tool step args: %v", err)
	}
	results, _ := r.dispatch(ctx, step.Name, args, nil)
	for _, result := range results {
		text := result.Text()
		if len(text) > longTextBytes {
			scenariof(lines, "tool %s result %d bytes sha256 %x error=%t", step.Name, len(text), sha256.Sum256([]byte(text)), result.IsError)
			continue
		}
		scenariof(lines, "tool %s result %q error=%t", step.Name, text, result.IsError)
	}
	if len(results) == 0 {
		scenariof(lines, "tool %s returned no result", step.Name)
	}
}

// runToolCallEvent emits the tool_call event the runner emits before a call
// and records what its handlers decided: a failure (as ErrorContains reads it),
// a block with its reason, or that the call goes ahead.
func (r *scenarioRig) runToolCallEvent(ctx context.Context, step sdkscenarios.Step, lines *[]string) {
	event := extension.CustomToolCallEvent{
		ToolCallEventBase: extension.ToolCallEventBase{Type: "tool_call", ToolCallID: "call-1"},
		ToolName:          step.Name,
		Input:             map[string]any{},
	}
	result, err := r.runner().EmitToolCall(ctx, event)
	switch {
	case err != nil && step.ErrorContains != "" && strings.Contains(err.Error(), step.ErrorContains):
		scenariof(lines, "tool_call %s error contains %q", step.Name, step.ErrorContains)
	case err != nil:
		scenariof(lines, "tool_call %s error: %s", step.Name, firstLine(err.Error()))
	case result != nil && result.Block:
		scenariof(lines, "tool_call %s blocked: %s", step.Name, result.Reason)
	default:
		scenariof(lines, "tool_call %s proceeds", step.Name)
	}
}

// abortRunInCommand starts a run, runs the command Name (one that waits for
// ctx.signal), aborts the run while the command is still in flight and records
// what the command wrote once it ended.
func (r *scenarioRig) abortRunInCommand(ctx context.Context, step sdkscenarios.Step, lines *[]string) {
	ext := r.primary()
	command, ok := ext.Command(step.Name)
	if !ok {
		r.t.Fatalf("command %s is not registered", step.Name)
	}
	path := filepath.Join(r.dir, "result-"+step.Name)
	r.signal.begin()
	r.bridge.RunSignalChanged()
	done := make(chan error, 1)
	go func() { done <- command.Handler(ctx, path) }()
	r.waitForMarker(ctx, ".started-"+step.Name)
	// The agent reports only a run's begin and end (agent.notifyRunSignalObservers); the abort reaches a runtime through the host's watch of the run's signal (runSignals.frame), so the step reports nothing here.
	r.signal.abort()
	select {
	case err := <-done:
		if err != nil {
			scenariof(lines, "command %s error: %s", step.Name, firstLine(err.Error()))
			return
		}
	case <-time.After(30 * time.Second):
		scenariof(lines, "command %s never saw the abort", step.Name)
		return
	}
	body, _ := os.ReadFile(path)
	scenariof(lines, "command %s %s", step.Name, body)
	r.signal.end()
	r.bridge.RunSignalChanged()
}

// emitEvent has the runner emit the event Name with the payload Args and
// records every line the EventRecord handlers appended, in the order they ran,
// each payload as the handler saw it.
func (r *scenarioRig) emitEvent(ctx context.Context, step sdkscenarios.Step, lines *[]string) {
	var event extension.ExtensionEvent
	switch step.Name {
	case "session_start":
		event = decodeEvent[extension.SessionStartEvent](r.t, step)
	case "session_shutdown":
		event = decodeEvent[extension.SessionShutdownEvent](r.t, step)
	case "turn_start":
		event = decodeEvent[extension.TurnStartEvent](r.t, step)
	default:
		r.t.Fatalf("emit-event step %q is not one the rig builds", step.Name)
	}
	path := filepath.Join(r.dir, ".events")
	_ = os.Remove(path)
	if _, err := r.runner().Emit(ctx, event); err != nil {
		scenariof(lines, "emit %s error: %s", step.Name, firstLine(err.Error()))
		return
	}
	body, _ := os.ReadFile(path)
	for line := range strings.SplitSeq(strings.TrimSuffix(string(body), "\n"), "\n") {
		if line == "" {
			scenariof(lines, "emit %s reached no handler", step.Name)
			return
		}
		tag, payload, _ := strings.Cut(line, ":")
		scenariof(lines, "emit %s handler %s saw %s", step.Name, tag, canonicalJSON(r.t, []byte(payload)))
	}
}

// decodeEvent builds the event of type E named by the step from its JSON payload.
func decodeEvent[E extension.ExtensionEvent](t *testing.T, step sdkscenarios.Step) E {
	t.Helper()
	body := map[string]any{}
	if err := json.Unmarshal([]byte(step.Args), &body); err != nil {
		t.Fatalf("emit-event step args: %v", err)
	}
	body["type"] = step.Name
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var event E
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatalf("emit-event step %s: %v", step.Name, err)
	}
	return event
}

// runSignal moves the host's run state: Name "begin" starts a run, "abort"
// aborts it and "end" finishes it, and the host reports each change as it does
// when its agent starts, aborts and ends a run.
func (r *scenarioRig) runSignal(step sdkscenarios.Step) {
	switch step.Name {
	case "begin":
		r.signal.begin()
	case "abort":
		r.signal.abort()
	case "end":
		r.signal.end()
	default:
		r.t.Fatalf("run-signal step name %q", step.Name)
	}
	r.bridge.RunSignalChanged()
}

// runInputEvent emits the input event for the user's text Name and records what
// the chained handlers made of it: that one handled it, the text after the
// transforms, or that no handler changed it.
func (r *scenarioRig) runInputEvent(ctx context.Context, step sdkscenarios.Step, lines *[]string) {
	result, err := r.runner().EmitInput(ctx, step.Name, nil, extension.InputSourceUser, "")
	if err != nil {
		scenariof(lines, "input %q error: %s", step.Name, firstLine(err.Error()))
		return
	}
	switch v := result.(type) {
	case extension.InputEventResultHandled:
		scenariof(lines, "input %q handled", step.Name)
	case extension.InputEventResultTransform:
		scenariof(lines, "input %q becomes %q", step.Name, v.Text)
	default:
		scenariof(lines, "input %q unchanged", step.Name)
	}
}

// runBeforeAgentStartEvent emits before_agent_start for the prompt Name and
// records what the chained handlers made of it: the forced system prompt, if one
// returned a prompt, and the custom messages they returned in order.
func (r *scenarioRig) runBeforeAgentStartEvent(ctx context.Context, step sdkscenarios.Step, lines *[]string) {
	result, err := r.runner().EmitBeforeAgentStart(ctx, extension.UserPromptTrigger(step.Name, nil), extension.BuildSystemPromptOptions{})
	switch {
	case err != nil:
		scenariof(lines, "before_agent_start %q error: %s", step.Name, firstLine(err.Error()))
	case result == nil:
		scenariof(lines, "before_agent_start %q unchanged", step.Name)
	default:
		prompt := "none"
		if result.SystemPrompt != nil {
			prompt = strconv.Quote(*result.SystemPrompt)
		}
		var messages []string
		for _, m := range result.Messages {
			messages = append(messages, fmt.Sprintf("%s/%v/display=%t", m.CustomType, m.Content, m.Display))
		}
		scenariof(lines, "before_agent_start %q systemPrompt=%s messages=%v", step.Name, prompt, messages)
	}
}

// runToolResultEvent emits the tool_result event for a call whose result is the
// text "original" and records what the chained handlers made of it: the content
// text, the error flag and the details, or that no handler changed anything.
func (r *scenarioRig) runToolResultEvent(ctx context.Context, step sdkscenarios.Step, lines *[]string) {
	event := extension.CustomToolResultEvent{
		ToolResultEventBase: extension.ToolResultEventBase{
			Type: "tool_result", ToolCallID: "call-1", Input: map[string]any{},
			Content: []any{map[string]any{"type": "text", "text": "original"}},
		},
		ToolName: step.Name,
	}
	result, err := r.runner().EmitToolResult(ctx, event)
	switch {
	case err != nil:
		scenariof(lines, "tool_result %s error: %s", step.Name, firstLine(err.Error()))
	case result == nil:
		scenariof(lines, "tool_result %s unchanged", step.Name)
	default:
		text := ""
		if len(result.Content) > 0 {
			if block, ok := result.Content[0].(map[string]any); ok {
				text, _ = block["text"].(string)
			}
		}
		// Pi's result carries the event's flag, false unless a handler set it; Pig's nil leaves the call's own flag.
		isError := result.IsError != nil && *result.IsError
		details, _ := json.Marshal(result.Details)
		scenariof(lines, "tool_result %s content=%q isError=%t details=%s", step.Name, text, isError, details)
	}
}

// runToolResult dispatches one call of a tool through the agent and records the
// whole result the model's message carries: the content blocks, the error flag
// and the details.
func (r *scenarioRig) runToolResult(ctx context.Context, step sdkscenarios.Step, lines *[]string) {
	var args map[string]any
	if err := json.Unmarshal([]byte(step.Args), &args); err != nil {
		r.t.Fatalf("tool-result step args: %v", err)
	}
	results, _ := r.dispatch(ctx, step.Name, args, nil)
	if len(results) == 0 {
		scenariof(lines, "tool %s returned no result", step.Name)
		return
	}
	result := results[0]
	var blocks []string
	for _, block := range result.Content {
		body, err := json.Marshal(block)
		if err != nil {
			r.t.Fatal(err)
		}
		blocks = append(blocks, canonicalJSON(r.t, body))
	}
	details := "absent"
	switch {
	case result.Details != nil:
		body, err := json.Marshal(result.Details)
		if err != nil {
			r.t.Fatal(err)
		}
		details = canonicalJSON(r.t, body)
	case result.DetailsNull:
		details = "null"
	}
	shown := strings.Join(blocks, " ")
	if step.ErrorContains != "" && result.IsError && strings.Contains(shown, step.ErrorContains) {
		shown = "<text contains " + strconv.Quote(step.ErrorContains) + ">"
	}
	scenariof(lines, "tool %s blocks=%d %s error=%t details=%s", step.Name, len(result.Content), shown, result.IsError, details)
}

// dispatch has the model call tool once and returns the tool results. When
// aborted is non-nil it runs until the tool wrote its started marker, cancels
// the run, and reports how long the abort took.
func (r *scenarioRig) dispatch(ctx context.Context, tool string, args map[string]any, aborted *time.Duration) ([]*agent.ToolResultMessage, error) {
	r.t.Helper()
	tools, errs := coding.BridgeNewRunnerTools(r.runner().Tools())
	if len(errs) != 0 {
		r.t.Fatal(errs)
	}
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	provider.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall(tool, args, &ai.FauxToolCallOptions{ID: "call-1"})}, StopReason: "toolUse"}),
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}),
	})
	a := mustNewAgent(agent.AgentOptions{Model: &ai.Model{ID: "faux-1", Provider: provider}, Tools: tools})
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type outcome struct {
		messages []agent.AgentMessage
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		messages, err := a.Send(runCtx, "go")
		done <- outcome{messages, err}
	}()
	var started time.Time
	if aborted != nil {
		r.waitForMarker(ctx, ".started-"+tool)
		started = time.Now()
		cancel()
	}
	var result outcome
	select {
	case result = <-done:
	case <-ctx.Done():
		r.t.Fatalf("the run of tool %s did not end: %v", tool, ctx.Err())
	}
	if aborted != nil {
		*aborted = time.Since(started)
	}
	var results []*agent.ToolResultMessage
	for _, message := range result.messages {
		if message.ToolResult != nil {
			results = append(results, message.ToolResult)
		}
	}
	return results, result.err
}

// waitForMarker waits for the file a driver tool or command writes when it starts.
func (r *scenarioRig) waitForMarker(ctx context.Context, name string) {
	r.t.Helper()
	path := filepath.Join(r.dir, name)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			r.t.Fatalf("%s never started: %v", name, ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// graceBounds bound how long an abort of a tool that never looks at its signal
// may take. The owner's rule (2026-10-09) signals the tool, waits about three
// seconds and then answers "Operation aborted".
const (
	graceMin = 2 * time.Second
	graceMax = 20 * time.Second
)

// abortTool aborts a run while the tool is in flight and records the tool
// result the model sees and whether the abort waited out the grace period.
func (r *scenarioRig) abortTool(ctx context.Context, step sdkscenarios.Step, lines *[]string) {
	var elapsed time.Duration
	results, _ := r.dispatch(ctx, step.Name, map[string]any{}, &elapsed)
	for _, result := range results {
		scenariof(lines, "abort %s result %q error=%t", step.Name, result.Text(), result.IsError)
	}
	if len(results) == 0 {
		scenariof(lines, "abort %s returned no result", step.Name)
	}
	scenariof(lines, "abort %s grace=%s", step.Name, graceVerdict(elapsed))
}

func graceVerdict(elapsed time.Duration) string {
	switch {
	case elapsed < graceMin:
		return "none"
	case elapsed <= graceMax:
		return "waited"
	default:
		return "too long"
	}
}

// reload swaps in the step's Spec, reloads the host and records what it holds.
func (r *scenarioRig) reload(step sdkscenarios.Step, lines *[]string) {
	if step.Spec != nil {
		writeSpec(r.t, filepath.Join(r.dir, scenarioFile), *step.Spec)
	}
	loaded, err := r.host.Reload(r.t.Context())
	if err != nil {
		scenariof(lines, "reload error: %s", firstLine(err.Error()))
		return
	}
	r.loaded = loaded
	scenariof(lines, "reload loaded=%d", len(loaded))
}

// endInput starts a command that never finishes, ends the host's input as a
// print or RPC mode does at stdin's end, and records how many commands the
// host counts as unanswered (Pi leaves those a hung command makes).
func (r *scenarioRig) endInput(ctx context.Context, step sdkscenarios.Step, lines *[]string) {
	windows := make(chan int, 16)
	r.host.SetCommandWindowHandler(func(suspended int) {
		select {
		case windows <- suspended:
		default:
		}
	})
	command, ok := r.primary().Commands[step.Name]
	if !ok {
		r.t.Fatalf("command %s is not registered", step.Name)
	}
	go func() { _ = command.Handler(r.t.Context(), "") }()
	r.waitForMarker(ctx, ".started-"+step.Name)
	r.host.EndInput()
	for {
		select {
		case suspended := <-windows:
			if suspended == 1 {
				scenariof(lines, "input ended: unanswered commands=%d", suspended)
				return
			}
		case <-ctx.Done():
			scenariof(lines, "input ended: the host never counted the hung command")
			return
		}
	}
}

// runShortcut fires a shortcut handler of the primary extension and records the
// marker the driver's handler wrote.
func (r *scenarioRig) runShortcut(ctx context.Context, step sdkscenarios.Step, lines *[]string) {
	var shortcut extension.ExtensionShortcut
	ok := false
	ext := r.primary()
	for _, registered := range ext.RegisteredShortcuts() {
		if registered.Shortcut == extension.KeyID(step.Name) {
			shortcut, ok = registered, true
		}
	}
	if !ok {
		scenariof(lines, "shortcut %s <not registered>", step.Name)
		return
	}
	if err := shortcut.Handler(ctx); err != nil {
		scenariof(lines, "shortcut %s error: %s", step.Name, firstLine(err.Error()))
		return
	}
	body, err := os.ReadFile(filepath.Join(r.dir, ".shortcut-fired"))
	if err != nil {
		scenariof(lines, "shortcut %s fired nothing", step.Name)
		return
	}
	scenariof(lines, "shortcut %s fired %s", step.Name, body)
}

// runFooterState changes the host's footer data, broadcasts the new state to the extensions, waits until the callbacks the extension subscribed ran step.Changes times in all, and records the branches they were told about.
func (r *scenarioRig) runFooterState(step sdkscenarios.Step, lines *[]string) {
	var statuses map[string]string
	if step.Args != "" {
		if err := json.Unmarshal([]byte(step.Args), &statuses); err != nil {
			r.t.Fatalf("footer-state step statuses: %v", err)
		}
	}
	r.footer.set(step.Name, statuses, step.Count)
	r.host.BroadcastStateUpdate()
	path := filepath.Join(r.dir, ".branch-changes")
	var changes []string
	deadline := time.Now().Add(30 * time.Second)
	for {
		body, _ := os.ReadFile(path)
		changes = strings.Fields(string(body))
		if len(changes) >= step.Changes || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	scenariof(lines, "footer state branch changes: %s", strings.Join(changes, ","))
}
