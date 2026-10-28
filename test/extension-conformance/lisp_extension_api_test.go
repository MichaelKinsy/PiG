package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// The Lisp twins of the Python SDK's Pi 0.99.1 API rows (python_extension_api_test.go, python_extension_api_fidelity_test.go,
// python_extension_api_detached_call_test.go) for the members of the extension context and tool registration. Each row binds its assertion to a value only the
// Host or the Lisp SDK's answer can produce, as the Python rows do. A fixture is a Lisp extension file whose package is named after the extension.

func newLispAPIRig(t *testing.T, isolation string, actions *subprocess.HostCallbacks, fixtures ...pyAPIFixture) *pyAPIRig {
	t.Helper()
	return newAPIRig(t, "lisp", isolation, actions, fixtures...)
}

// lispAPIFixture is the source of the extension name: a package of that name with the factory new-extension; body is the factory's forms, and lines
// the helper that reports its values joined by "|" through ctx-notify.
func lispAPIFixture(name, helpers, body string) pyAPIFixture {
	return pyAPIFixture{name, fmt.Sprintf(`(defpackage %q (:use "COMMON-LISP"))
(in-package %q)
(defun lines (ctx &rest values)
  (pig:ctx-notify ctx (format nil "~{~A~^|~}" values)))
(defun bool (value) (if (eq value t) "true" "false"))
(defun csv (list) (format nil "~{~A~^,~}" list))
%s
(defun new-extension ()
  (let ((e (pig:make-extension %q)))
%s
    e))
`, name, name, helpers, name, body)}
}

var lispToolsFixture = lispAPIFixture("lispapi-tools", `
(defun prepare (loadout)
  (let ((grep (or (pig:tool-loadout-get-namespace loadout "grep") (pig:obj))))
    (pig:obj "descriptions"
             (pig:obj "read" (format nil "lisp: ~D callable, ~D registered, grep is ~A in ~A, absent is ~A, grep guidelines ~A, absent guidelines ~A"
                                     (length (pig:tool-loadout-callable loadout)) (length (pig:tool-loadout-registered loadout))
                                     (pig:tool-loadout-get-exposure loadout "grep") (pig:jget grep "name") (pig:tool-loadout-get-exposure loadout "absent")
                                     (pig:to-json (pig:tool-loadout-get-prompt-guidelines loadout "grep"))
                                     (pig:to-json (pig:tool-loadout-get-prompt-guidelines loadout "absent"))))
             "hiddenDeclarations" (coerce (loop for tool across (pig:tool-loadout-declared loadout)
                                                unless (equal (pig:jget tool "name") "read") collect (pig:jget tool "name"))
                                          'simple-vector))))

(defun reaches (args)
  (if (pig:jget args "all")
      "all"
      (let ((keys '())) (maphash (lambda (k v) (declare (ignore v)) (push (concatenate 'string "lisp:" k) keys)) args) (sort keys #'string<))))
`, `
    (pig:register-tool e "search" :label "Search" :description "Search docs" :parameters (pig:obj "type" "object")
      :output-schema (pig:from-json "{\"type\":\"object\",\"properties\":{\"rows\":{\"type\":\"array\"}}}")
      :exposure "codemode"
      :namespace (pig:from-json "{\"name\":\"mcp__lisp\",\"aliases\":[\"lisp\",\"lisp-docs\"],\"description\":\"Lisp server\",\"instructions\":\"Search the lisp docs.\"}")
      :annotations (pig:from-json "{\"readOnlyHint\":true,\"openWorldHint\":false}") :default-active :false
      :prepare-loadout #'prepare :reaches-tools #'reaches
      :execute (lambda (ctx args) (declare (ignore ctx args)) "unused"))
    (pig:register-tool e "plain" :label "Plain" :description "Plain" :parameters (pig:obj "type" "object")
      :execute (lambda (ctx args) (declare (ignore ctx args)) "plain"))
    (pig:tool e "structured" "Structured result" (pig:obj "type" "object")
      (lambda (ctx args) (declare (ignore ctx args))
        (pig:tool-result "model-visible text" :structured-content (pig:obj "rows" (vector 3 1 4)) :is-error t :details (pig:obj "k" "v"))))
    (pig:command e "settings" "" (lambda (ctx args) (declare (ignore args))
      (lines ctx "settings" (pig:jget (pig:ctx-get-settings ctx) "marker") (pig:to-json (pig:jget (pig:ctx-get-settings ctx) "nested")))))
`)

// types.ts:579-607, 601-607 and agent-session.ts:1480-1518: a tool's outputSchema, exposure, namespace, annotations and defaultActive reach the Host's definition; prepareLoadout runs in the extension against the loadout the Host builds, and its answer carries values computed from that loadout.
func TestLispSDKToolExposureFieldsAndPrepareLoadout(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newLispAPIRig(t, isolation, nil, lispToolsFixture)
		search := rig.exts["lispapi-tools"].Tools["search"].Definition
		if string(search.OutputSchema) != `{"type":"object","properties":{"rows":{"type":"array"}}}` || search.Exposure != extension.ToolExposureCodemode {
			t.Fatalf("outputSchema=%s exposure=%q", search.OutputSchema, search.Exposure)
		}
		if search.Namespace == nil || search.Namespace.Name != "mcp__lisp" || search.Namespace.Description != "Lisp server" || search.Namespace.Instructions != "Search the lisp docs." {
			t.Fatalf("namespace = %+v", search.Namespace)
		}
		if a := search.Annotations; a == nil || a.ReadOnlyHint == nil || !*a.ReadOnlyHint || a.OpenWorldHint == nil || *a.OpenWorldHint || a.DestructiveHint != nil || a.IdempotentHint != nil {
			t.Fatalf("annotations = %+v", search.Annotations)
		}
		if search.DefaultActive == nil || *search.DefaultActive {
			t.Fatalf("defaultActive = %v, want false", search.DefaultActive)
		}
		plain := rig.exts["lispapi-tools"].Tools["plain"].Definition
		if plain.PrepareLoadout != nil || plain.ReachesTools != nil || plain.Exposure != "" || plain.DefaultActive != nil || plain.OutputSchema != nil || plain.Namespace != nil || plain.Annotations != nil {
			t.Fatalf("an undeclared field was set on %+v", plain)
		}
		if search.PrepareLoadout == nil {
			t.Fatal("a tool that defines prepare-loadout has no PrepareLoadout")
		}
		if !slices.Equal(search.Namespace.Aliases, []string{"lisp", "lisp-docs"}) {
			t.Fatalf("namespace aliases = %v", search.Namespace.Aliases)
		}
		if search.ReachesTools == nil || plain.ReachesTools != nil {
			t.Fatalf("reachesTools: search %v, plain %v", search.ReachesTools != nil, plain.ReachesTools != nil)
		}
		if got := search.ReachesTools(json.RawMessage(`{"b":1,"a":2}`)); got.All || !slices.Equal(got.Words, []string{"lisp:a", "lisp:b"}) {
			t.Fatalf("reach = %+v, want words lisp:a, lisp:b", got)
		}
		if got := search.ReachesTools(json.RawMessage(`{"all":true}`)); !got.All {
			t.Fatalf("reach = %+v, want all", got)
		}
		read := extension.AgentTool{Name: "read", Label: "Read", Description: "Read", Parameters: json.RawMessage(`{"type":"object"}`)}
		grep := extension.AgentTool{Name: "grep", Label: "Grep", Description: "Grep", Parameters: json.RawMessage(`{"type":"object"}`)}
		changes := search.PrepareLoadout(extension.ToolLoadout{
			Declared: []extension.AgentTool{read, grep}, Callable: []extension.AgentTool{read}, Registered: []extension.AgentTool{read, grep},
			GetExposure: func(name string) extension.ToolExposure {
				if name == "grep" {
					return extension.ToolExposureDeferred
				}
				return extension.ToolExposureDirect
			},
			GetNamespace: func(name string) *extension.ToolNamespace {
				if name == "grep" {
					return &extension.ToolNamespace{Name: "search-ns"}
				}
				return nil
			},
			GetPromptGuidelines: func(name string) []string {
				if name == "grep" {
					return []string{"Use grep for patterns.", "Quote regexes."}
				}
				return nil
			},
		})
		wantDescription := `lisp: 1 callable, 2 registered, grep is deferred in search-ns, absent is direct, grep guidelines ["Use grep for patterns.","Quote regexes."], absent guidelines []`
		if changes == nil || changes.Descriptions["read"] != wantDescription || !slices.Equal(changes.HiddenDeclarations, []string{"grep"}) {
			t.Fatalf("changes = %+v, want description %q hiding grep", changes, wantDescription)
		}
	})
}

// agent/src/types.ts:424-446: structuredContent and isError survive the SDK, the wire and the Host's tool result.
func TestLispSDKToolResultCarriesStructuredContentAndIsError(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newLispAPIRig(t, isolation, nil, lispToolsFixture)
		result, err := rig.execute("lispapi-tools", "structured", "tc-structured")
		if err != nil {
			t.Fatal(err)
		}
		if string(result.StructuredContent) != `{"rows":[3,1,4]}` || !result.IsError || result.Text() != "model-visible text" {
			t.Fatalf("result = structured %s isError %v text %q", result.StructuredContent, result.IsError, result.Text())
		}
	})
}

// types.ts:1712 and loader.ts:411-413: getSettings returns the settings object the Host holds, read from the state the Host replicated.
func TestLispSDKGetSettings(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newLispAPIRig(t, isolation, &subprocess.HostCallbacks{GetSettings: func() extension.Settings {
			return extension.Settings{"marker": "lisp-settings-value-42", "nested": map[string]any{"b": []any{1, 2}, "a": true}}
		}}, lispToolsFixture)
		rig.command("lispapi-tools", "settings", "")
		rig.waitNotification(`settings|lisp-settings-value-42|{"a":true,"b":[1,2]}`)
	})
}

var lispNestFixture = lispAPIFixture("lispapi-nest", `
(defun nest (ctx args)
  (declare (ignore args))
  (let* ((seen '())
         (out (pig:ctx-execute-tool ctx "echo" (pig:obj "v" 7)
                                    :on-update (lambda (p) (push (pig:jref p "content" 0 "text") seen))))
         (names (csv (map 'list (lambda (tool) (pig:jget tool "name")) (pig:ctx-tools ctx)))))
    (format nil "~A|~A|~A|~A|~A" names (pig:jref out "toolCall" "id") (pig:jref out "result" "content" 0 "text")
            (bool (pig:jget out "isError")) (csv (reverse seen)))))

(defun cancel-nest (ctx args)
  (declare (ignore args))
  (let ((signal (pig:make-abort-signal)))
    (sb-thread:make-thread (lambda () (sleep 0.2) (pig:signal-abort signal)))
    (let ((out (pig:ctx-execute-tool ctx "wait" (pig:obj) :signal signal)))
      (format nil "isError=~A text=~A" (bool (pig:jget out "isError")) (pig:jref out "result" "content" 0 "text")))))

(defun outside (ctx)
  (handler-case (progn (pig:ctx-tools ctx) "tools available") (error (e) (princ-to-string e))))
`, `
    (pig:tool e "nest" "Runs echo" (pig:obj "type" "object") #'nest)
    (pig:tool e "cancel_nest" "Cancels wait" (pig:obj "type" "object") #'cancel-nest)
    (pig:command e "tools-outside" "" (lambda (ctx args) (declare (ignore args)) (lines ctx "outside" (outside ctx))))
    (pig:on e "tool_call" (lambda (ctx event) (lines ctx "tool_call" (pig:jget event "toolCallId") (pig:jget event "parentToolCallId")) nil))
    (pig:on e "tool_result" (lambda (ctx event)
      (lines ctx "tool_result" (pig:jget event "toolCallId") (pig:jget event "parentToolCallId") (pig:to-json (pig:jget event "structuredContent"))) nil))
    (pig:on e "provider_stream_event" (lambda (ctx event)
      (lines ctx "provider_stream_event" (pig:jget event "provider") (pig:jget event "api") (pig:jget event "model") (pig:to-json (pig:jget event "data"))) nil))
    (pig:on e "mcp_servers_change" (lambda (ctx event)
      (lines ctx "mcp_servers_change" (csv (map 'list (lambda (s) (pig:jget s "name")) (pig:jget event "servers")))) nil))
`)

// types.ts:367-395 and runner.ts:952-985: a tool's context lists the tools the Host reports as callable and runs another tool through the Host, which names the nested call after the calling one, streams partial results in order before the outcome, and returns the outcome untouched.
func TestLispSDKExecuteToolAndCallableTools(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		var mu sync.Mutex
		var seen []string
		actions := &subprocess.HostCallbacks{
			GetCallableTools: func() []extension.AgentTool {
				return []extension.AgentTool{{Name: "echo", Label: "Echo", Parameters: json.RawMessage(`{"type":"object"}`)}, {Name: "grep", Label: "Grep", Parameters: json.RawMessage(`{"type":"object"}`)}}
			},
			ExecuteTool: func(ctx context.Context, callerID, name string, args json.RawMessage, options extension.ExecuteToolOptions) (extension.AgentToolCallOutcome, error) {
				outcome := extension.AgentToolCallOutcome{ToolCall: ai.ToolCall{ID: callerID + "/1", Name: name, Arguments: ai.JsonObject{}}}
				switch name {
				case "echo":
					if update := options.OnUpdate; update != nil {
						var first error
						for _, step := range []string{"partial-one", "partial-two"} {
							if err := update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: step}}}); err != nil && first == nil {
								first = err
							}
						}
						if first != nil {
							return extension.AgentToolCallOutcome{}, first
						}
					}
					outcome.Result = agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "host ran echo " + string(args)}}}
				case "wait":
					<-ctx.Done()
					mu.Lock()
					seen = append(seen, "wait cancelled")
					mu.Unlock()
					outcome.IsError = true
					outcome.Result = agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "aborted"}}, IsError: true}
				}
				return outcome, nil
			},
		}
		rig := newLispAPIRig(t, isolation, actions, lispNestFixture)
		result, err := rig.execute("lispapi-nest", "nest", "call-7")
		if err != nil || result.IsError {
			t.Fatalf("nest = %+v, %v", result, err)
		}
		if want := `echo,grep|call-7/1|host ran echo {"v":7}|false|partial-one,partial-two`; result.Text() != want {
			t.Fatalf("nest text = %q, want %q", result.Text(), want)
		}
		result, err = rig.execute("lispapi-nest", "cancel_nest", "call-8")
		if err != nil {
			t.Fatal(err)
		}
		if result.Text() != "isError=true text=aborted" {
			t.Fatalf("cancel_nest text = %q", result.Text())
		}
		mu.Lock()
		defer mu.Unlock()
		if !slices.Equal(seen, []string{"wait cancelled"}) {
			t.Fatalf("host saw %v", seen)
		}
	})
}

// types.ts:385-395 (ExtensionToolContext only) and 1044-1219 (parentToolCallId, structuredContent), 699-709 and 884-890 (the new events): outside a tool call the context has no `tools`, tool events carry the parent id and structured content, and the new events reach the handler with the fields the Host set.
func TestLispSDKToolEventsAndNewEvents(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newLispAPIRig(t, isolation, nil, lispNestFixture)
		rig.command("lispapi-nest", "tools-outside", "")
		rig.waitNotification("outside|ctx-tools is only available while a tool runs")

		base := extension.ToolCallEventBase{Type: "tool_call", ToolCallID: "outer-1/2", ParentToolCallID: "outer-1"}
		if _, err := rig.runner.EmitToolCall(t.Context(), extension.CustomToolCallEvent{ToolCallEventBase: base, ToolName: "bash", Input: map[string]any{"command": "ls"}}); err != nil {
			t.Fatal(err)
		}
		rig.waitNotification("tool_call|outer-1/2|outer-1")

		if _, err := rig.runner.EmitToolResult(t.Context(), extension.CustomToolResultEvent{
			ToolResultEventBase: extension.ToolResultEventBase{Type: "tool_result", ToolCallID: "outer-1/3", ParentToolCallID: "outer-1", Input: map[string]any{}, Content: []any{}, StructuredContent: json.RawMessage(`{"rows":[9,8]}`)},
			ToolName:            "custom",
		}); err != nil {
			t.Fatal(err)
		}
		rig.waitNotification(`tool_result|outer-1/3|outer-1|{"rows":[9,8]}`)

		if _, err := rig.runner.Emit(t.Context(), extension.ProviderStreamEvent{Type: "provider_stream_event", Provider: "prov-x", API: "api-y", Model: "model-z", Data: map[string]any{"delta": []any{1, "two"}}}); err != nil {
			t.Fatal(err)
		}
		rig.waitNotification(`provider_stream_event|prov-x|api-y|model-z|{"delta":[1,"two"]}`)

		servers := []extension.RegisteredMcpServer{{Name: "one", ExtensionPath: "/x"}, {Name: "two", ExtensionPath: "/y"}}
		if _, err := rig.runner.Emit(t.Context(), extension.McpServersChangeEvent{Type: "mcp_servers_change", Servers: servers}); err != nil {
			t.Fatal(err)
		}
		rig.waitNotification("mcp_servers_change|one,two")
	})
}

var lispPreviewFixture = lispAPIFixture("lispapi-preview", "", `
    (pig:tool e "previewed" "Returns a result with a preview" (pig:obj "type" "object")
      (lambda (ctx args) (declare (ignore ctx args)) (pig:tool-result "full output for the model" :preview "preview-lisp-9b2e")))
    (pig:tool e "plain" "Returns a result without a preview" (pig:obj "type" "object")
      (lambda (ctx args) (declare (ignore ctx args)) "plain text"))
`)

// docs/additive-features.md (tool result preview): the collapsed-view summary a Lisp tool returns in its result reaches the host's tool result, and the model-facing content stays the full text.
func TestLispSDKToolResultPreview(t *testing.T) {
	t.Parallel()
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newLispAPIRig(t, isolation, nil, lispPreviewFixture)
		previewed, err := rig.execute("lispapi-preview", "previewed", "call-p")
		if err != nil {
			t.Fatal(err)
		}
		if previewed.Preview != "preview-lisp-9b2e" || previewed.Text() != "full output for the model" {
			t.Fatalf("previewed preview=%q text=%q", previewed.Preview, previewed.Text())
		}
		plain, err := rig.execute("lispapi-preview", "plain", "call-q")
		if err != nil {
			t.Fatal(err)
		}
		if plain.Preview != "" || plain.Text() != "plain text" {
			t.Fatalf("plain preview=%q text=%q", plain.Preview, plain.Text())
		}
	})
}

var lispOwnSignalFixture = lispAPIFixture("lispapi-own-signal", "", `
    (pig:tool e "own_signal" "Runs wait_release under its own signal" (pig:obj "type" "object")
      (lambda (ctx args) (declare (ignore args))
        (pig:ctx-execute-tool ctx "wait_release" (pig:obj) :signal (pig:make-abort-signal))
        "done"))
`)

// runner.ts:980 (`signal: options.signal ?? signal`): a nested call given its own signal is not cancelled with the calling tool's request; it runs until it ends on its own.
func TestLispSDKExecuteToolOwnSignalOutlivesTheCallingRequest(t *testing.T) {
	t.Parallel()
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		started, ended, release, owned := make(chan struct{}, 1), make(chan string, 1), make(chan struct{}), make(chan bool, 1)
		actions := &subprocess.HostCallbacks{
			ExecuteTool: func(ctx context.Context, callerID, name string, _ json.RawMessage, _ extension.ExecuteToolOptions) (extension.AgentToolCallOutcome, error) {
				outcome := extension.AgentToolCallOutcome{ToolCall: ai.ToolCall{ID: callerID + "/1", Name: name, Arguments: ai.JsonObject{}}}
				owned <- extension.OwnsSignal(ctx)
				started <- struct{}{}
				select {
				case <-ctx.Done():
					ended <- "Aborted"
					outcome.IsError = true
				case <-release:
					ended <- "released"
				}
				return outcome, nil
			},
		}
		rig := newLispAPIRig(t, isolation, actions, lispOwnSignalFixture)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		def := rig.exts["lispapi-own-signal"].Tools["own_signal"]
		go func() { _, _ = def.Definition.Execute(ctx, "call-o", json.RawMessage(`{}`), nil) }()
		select {
		case <-started:
		case <-time.After(20 * time.Second):
			t.Fatal("the nested call did not start")
		}
		if !<-owned {
			t.Error("the nested call with an explicit signal was not marked as running with it")
		}
		cancel()
		select {
		case got := <-ended:
			t.Fatalf("the nested call ended with the calling request: %s", got)
		case <-time.After(500 * time.Millisecond):
		}
		close(release)
		select {
		case got := <-ended:
			if got != "released" {
				t.Fatalf("the nested call ended with %q, want it released", got)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("the nested call did not end")
		}
	})
}

var lispLiveToolsFixture = lispAPIFixture("lispapi-live", `
(defun live-tools (ctx args)
  (declare (ignore args))
  (flet ((names () (csv (map 'list (lambda (tool) (pig:jget tool "name")) (pig:ctx-tools ctx)))))
    (let* ((before (names)) (ignored (pig:ctx-set-active-tools ctx '("only"))) (after (names)))
      (declare (ignore ignored))
      (format nil "~A|~A|~A" before after (names)))))
`, `
    (pig:tool e "live_tools" "Reads ctx-tools around ctx-set-active-tools" (pig:obj "type" "object") #'live-tools)
`)

// runner.ts:958-961: the ctx.tools getter reads the callable tools when it is read, so a read after setActiveTools inside the same handler sees the change.
func TestLispSDKCtxToolsIsLiveInsideOneHandler(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		var mu sync.Mutex
		active := []string{"a", "b", "c"}
		actions := &subprocess.HostCallbacks{
			SetActiveTools: func(names []string) {
				mu.Lock()
				active = slices.Clone(names)
				mu.Unlock()
			},
			GetCallableTools: func() []extension.AgentTool {
				mu.Lock()
				defer mu.Unlock()
				var tools []extension.AgentTool
				for _, name := range active {
					tools = append(tools, extension.AgentTool{Name: name, Label: name, Parameters: json.RawMessage(`{"type":"object"}`)})
				}
				return tools
			},
		}
		rig := newLispAPIRig(t, isolation, actions, lispLiveToolsFixture)
		result, err := rig.execute("lispapi-live", "live_tools", "call-l")
		if err != nil || result.IsError {
			t.Fatalf("live_tools = %+v, %v", result, err)
		}
		if want := "a,b,c|only|only"; result.Text() != want {
			t.Fatalf("ctx-tools read %q, want %q", result.Text(), want)
		}
	})
}

var lispNestedThrowFixture = lispAPIFixture("lispapi-throw", `
(defun nest-throw (ctx args)
  (declare (ignore args))
  (let ((seen '()))
    (handler-case
        (progn (pig:ctx-execute-tool ctx "slow" (pig:obj)
                                     :on-update (lambda (partial)
                                                  (push (pig:jref partial "content" 0 "text") seen)
                                                  (when (= (length seen) 1) (error "callback boom"))))
               (format nil "no rejection after ~A" (csv (reverse seen))))
      (error (e) (format nil "rejected: ~A after ~A" e (csv (reverse seen)))))))
`, `
    (pig:tool e "nest_throw" "Signals from on-update" (pig:obj "type" "object") #'nest-throw)
    (pig:tool e "nest_duration" "Reports the durationMs of a nested outcome" (pig:obj "type" "object")
      (lambda (ctx args) (declare (ignore args)) (format nil "~A" (pig:jget (pig:ctx-execute-tool ctx "timed" (pig:obj)) "durationMs"))))
`)

// types.ts:454 and nested-tool-calls.ts:246: executeTool's outcome carries the nested call's durationMs.
func TestLispSDKExecuteToolOutcomeCarriesDuration(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		actions := &subprocess.HostCallbacks{
			ExecuteTool: func(context.Context, string, string, json.RawMessage, extension.ExecuteToolOptions) (extension.AgentToolCallOutcome, error) {
				return extension.AgentToolCallOutcome{DurationMs: new(int64(4321))}, nil
			},
		}
		rig := newLispAPIRig(t, isolation, actions, lispNestedThrowFixture)
		result, err := rig.execute("lispapi-throw", "nest_duration", "call-d")
		if err != nil || result.IsError || result.Text() != "4321" {
			t.Fatalf("nest_duration = %+v, %v, want 4321", result, err)
		}
	})
}

// nested-tool-calls.ts:219-248 and agent-loop.ts:820-849: an error from the caller's on-update rejects the nested call after the tool returned, every partial result still reaches on-update, and the call never reaches afterToolCall or tool_execution_end.
func TestLispSDKExecuteToolOnUpdateErrorRejectsTheNestedCall(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		var mu sync.Mutex
		var sinkResults []string
		var completed []string
		actions := &subprocess.HostCallbacks{
			ExecuteTool: func(_ context.Context, callerID, name string, _ json.RawMessage, options extension.ExecuteToolOptions) (extension.AgentToolCallOutcome, error) {
				sink := options.OnUpdate
				if sink == nil {
					return extension.AgentToolCallOutcome{DurationMs: new(int64(4321))}, nil
				}
				var first error
				for _, step := range []string{"one", "two"} {
					err := sink(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: step}}, Details: map[string]any{"step": step}})
					mu.Lock()
					if err != nil {
						sinkResults = append(sinkResults, step+": "+err.Error())
					} else {
						sinkResults = append(sinkResults, step+": ok")
					}
					mu.Unlock()
					if err != nil && first == nil {
						first = err
					}
				}
				if first != nil {
					return extension.AgentToolCallOutcome{}, first
				}
				mu.Lock()
				completed = append(completed, name)
				mu.Unlock()
				return extension.AgentToolCallOutcome{ToolCall: ai.ToolCall{ID: callerID + "/1", Name: name, Arguments: ai.JsonObject{}}, Result: agent.AgentToolResult{}}, nil
			},
		}
		rig := newLispAPIRig(t, isolation, actions, lispNestedThrowFixture)
		result, err := rig.execute("lispapi-throw", "nest_throw", "call-t")
		if err != nil || result.IsError {
			t.Fatalf("nest_throw = %+v, %v", result, err)
		}
		if want := "rejected: callback boom after one,two"; result.Text() != want {
			t.Fatalf("the extension saw %q, want %q", result.Text(), want)
		}
		mu.Lock()
		defer mu.Unlock()
		if want := []string{"one: callback boom", "two: ok"}; !slices.Equal(sinkResults, want) {
			t.Fatalf("the update sink returned %v, want %v", sinkResults, want)
		}
		if len(completed) != 0 {
			t.Fatalf("the call completed %v although its update callback signalled", completed)
		}
	})
}

var lispDetachedCallFixture = lispAPIFixture("lispapi-detached", "", `
    (pig:tool e "detached_call" "Starts a nested call on a thread and returns once the host reports it pending" (pig:obj "type" "object")
      (lambda (ctx args)
        (let ((directory (pig:jget args "dir")))
          (sb-thread:make-thread
           (lambda ()
             (let ((record (handler-case (format nil "ok:~A" (pig:jref (pig:ctx-execute-tool ctx "wait_release" (pig:obj)) "result" "content" 0 "text"))
                             (error (e) (format nil "err:~A" e)))))
               (with-open-file (out (concatenate 'string directory "/out") :direction :output :if-exists :supersede) (write-string record out)))))
          (loop repeat 2000
                do (when (probe-file (concatenate 'string directory "/started")) (return-from nil "returned"))
                   (sleep 0.005)
                finally (error "the nested call never started")))))
`)

// A handler that starts a host call on a thread and returns leaves the call running, as Pi's un-awaited call does.
func TestLispSDKHostCallStartedByAHandlerOutlivesItsResponse(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		started, release, ended := make(chan struct{}, 1), make(chan struct{}), make(chan string, 1)
		actions := &subprocess.HostCallbacks{
			ExecuteTool: func(ctx context.Context, callerID, name string, _ json.RawMessage, _ extension.ExecuteToolOptions) (extension.AgentToolCallOutcome, error) {
				outcome := extension.AgentToolCallOutcome{ToolCall: ai.ToolCall{ID: callerID + "/1", Name: name, Arguments: ai.JsonObject{}}}
				started <- struct{}{}
				select {
				case <-ctx.Done():
					ended <- "Aborted"
					outcome.IsError = true
				case <-release:
					ended <- "released"
					outcome.Result = agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "released"}}}
				}
				return outcome, nil
			},
		}
		rig := newLispAPIRig(t, isolation, actions, lispDetachedCallFixture)
		dir := t.TempDir()
		args, err := json.Marshal(map[string]string{"dir": dir})
		if err != nil {
			t.Fatal(err)
		}
		def := rig.exts["lispapi-detached"].Tools["detached_call"]
		type done struct {
			text string
			err  error
		}
		returned := make(chan done, 1)
		go func() {
			result, err := def.Definition.Execute(t.Context(), "call-d", args, nil)
			returned <- done{result.Text(), err}
		}()
		select {
		case <-started:
		case <-time.After(20 * time.Second):
			t.Fatal("the thread's nested call never reached the host")
		}
		if err := os.WriteFile(filepath.Join(dir, "started"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-returned:
			if got.err != nil || got.text != "returned" {
				t.Fatalf("detached_call = %q, %v", got.text, got.err)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("the handler never returned")
		}
		close(release)
		select {
		case got := <-ended:
			if got != "released" {
				t.Fatalf("the nested call ended %q, want released", got)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("the nested call never ended")
		}
		var record []byte
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			if data, err := os.ReadFile(filepath.Join(dir, "out")); err == nil && len(data) > 0 {
				record = data
				break
			}
		}
		if string(record) != "ok:released" {
			t.Fatalf("the thread's call recorded %q, want ok:released", record)
		}
	})
}
