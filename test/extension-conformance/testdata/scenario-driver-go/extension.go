// Package driver is the Go scenario driver: it registers, through the Go SDK's
// public API, the extension the scenario Spec describes (sdkscenarios.Spec). The
// Node, Python and Rust drivers read the same file and do the same, so one
// scenario table exercises every SDK. Keep each driver a straight reading of the
// Spec: behavior belongs in the scenario, not here.
package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

type spec struct {
	Providers []providerSpec `json:"providers"`
	Tools     []toolSpec     `json:"tools"`
	Commands  []commandSpec  `json:"commands"`
	Shortcuts []shortcutSpec `json:"shortcuts"`
	Flags     []flagSpec     `json:"flags"`
	Events    []eventSpec    `json:"events"`
}

type toolSpec struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Behavior    string `json:"behavior"`
	Schema      string `json:"schema"`
}

type commandSpec struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Op          string `json:"op"`
	Arg         string `json:"arg"`
}

type shortcutSpec struct {
	Key         string `json:"key"`
	Description string `json:"description"`
}

type eventSpec struct {
	Name string `json:"name"`
	Op   string `json:"op"`
	Tag  string `json:"tag"`
}

type flagSpec struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Default     any    `json:"default"`
}

type providerSpec struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	APIKey *apiKeySpec `json:"apiKey"`
	OAuth  *oauthSpec  `json:"oauth"`
}

type apiKeySpec struct {
	Name        string    `json:"name"`
	CheckSource string    `json:"checkSource"`
	Login       *flowSpec `json:"login"`
}

type oauthSpec struct {
	Name         string   `json:"name"`
	LoginLabel   string   `json:"loginLabel"`
	Subscription bool     `json:"subscription"`
	Login        flowSpec `json:"login"`
	Refresh      string   `json:"refresh"`
}

type flowSpec struct {
	Steps  []stepSpec `json:"steps"`
	Result string     `json:"result"`
}

type stepSpec struct {
	Notify   string `json:"notify"`
	Prompt   string `json:"prompt"`
	DeviceID bool   `json:"deviceId"`
	As       string `json:"as"`
}

var placeholder = regexp.MustCompile(`\$\{(\w+)\}`)

// substitute replaces "${name}" in every string of value with the answer kept as name.
func substitute(value any, vars map[string]string) any {
	switch value := value.(type) {
	case string:
		return placeholder.ReplaceAllStringFunc(value, func(match string) string { return vars[placeholder.FindStringSubmatch(match)[1]] })
	case []any:
		for i := range value {
			value[i] = substitute(value[i], vars)
		}
	case map[string]any:
		for key := range value {
			value[key] = substitute(value[key], vars)
		}
	case *sdk.OrderedObject:
		pairs := []any{}
		for _, key := range value.Keys() {
			item, _ := value.Get(key)
			pairs = append(pairs, key, substitute(item, vars))
		}
		return sdk.NewOrderedObject(pairs...)
	}
	return value
}

// decodeCredential reads a credential the way the host sends it, with its keys in order.
func decodeCredential(text string) (*sdk.Credential, error) {
	credential := &sdk.Credential{}
	return credential, json.Unmarshal([]byte(text), credential)
}

func decode(text string) (map[string]any, error) {
	var object map[string]any
	return object, json.Unmarshal([]byte(text), &object)
}

func runFlow(flow flowSpec, interaction sdk.AuthInteraction, options sdk.LoginOptions) (*sdk.Credential, error) {
	vars := map[string]string{}
	for _, step := range flow.Steps {
		if step.DeviceID {
			vars[step.As] = "<none>"
			if options.GetDeviceID != nil {
				id, err := options.GetDeviceID()
				if err != nil {
					return nil, err
				}
				vars[step.As] = id
			}
			continue
		}
		if step.Notify != "" {
			event, err := decode(step.Notify)
			if err != nil {
				return nil, err
			}
			if err := interaction.Notify(substitute(event, vars).(map[string]any)); err != nil {
				return nil, err
			}
			continue
		}
		prompt, err := decode(step.Prompt)
		if err != nil {
			return nil, err
		}
		answer, err := interaction.Prompt(prompt)
		if err != nil {
			return nil, err
		}
		vars[step.As] = answer
	}
	result, err := decodeCredential(flow.Result)
	if err != nil {
		return nil, err
	}
	for _, key := range result.Keys() {
		value, _ := result.Get(key)
		result.Set(key, substitute(value, vars))
	}
	return result, nil
}

func providerObject(provider providerSpec) *sdk.Provider {
	unused := func(map[string]any, map[string]any, sdk.ProviderStreamOptions) (*sdk.ModelEventStream, error) {
		return nil, errors.New("the scenario provider streams nothing")
	}
	model := map[string]any{"id": provider.ID + "-model", "name": provider.Name + " model", "provider": provider.ID, "api": "openai-completions", "baseUrl": "http://127.0.0.1:9", "reasoning": false, "input": []string{"text"}, "cost": map[string]int{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 4000, "maxTokens": 100}
	var auth sdk.ProviderAuth
	if method := provider.APIKey; method != nil {
		auth.APIKey = &sdk.APIKeyAuth{Name: method.Name, Resolve: func(input sdk.APIKeyAuthInput) (*sdk.AuthResult, error) {
			key := input.Credential.String("key")
			if key == "" {
				return nil, nil
			}
			source := "stored credential"
			return &sdk.AuthResult{Auth: map[string]any{"apiKey": "resolved-" + key}, Source: &source}, nil
		}}
		if source := method.CheckSource; source != "" {
			auth.APIKey.Check = func(sdk.APIKeyAuthInput) (*sdk.AuthCheck, error) {
				return &sdk.AuthCheck{Type: "api_key", Source: &source}, nil
			}
		}
		if login := method.Login; login != nil {
			auth.APIKey.Login = func(interaction sdk.AuthInteraction) (*sdk.Credential, error) {
				return runFlow(*login, interaction, sdk.LoginOptions{})
			}
		}
	}
	if method := provider.OAuth; method != nil {
		oauth := &sdk.OAuthAuth{
			Name: method.Name,
			Login: func(interaction sdk.AuthInteraction, options sdk.LoginOptions) (*sdk.Credential, error) {
				return runFlow(method.Login, interaction, options)
			},
			// The refresh returns { ...credential, ...fields }: a refreshed key keeps its position and a new one goes last.
			Refresh: func(credential *sdk.Credential, _ context.Context) (*sdk.Credential, error) {
				if method.Refresh == "" {
					return credential, nil
				}
				fields, err := decodeCredential(method.Refresh)
				if err != nil {
					return nil, err
				}
				refreshed := credential.Clone()
				for _, key := range fields.Keys() {
					value, _ := fields.Get(key)
					refreshed.Set(key, value)
				}
				return refreshed, nil
			},
			ToAuth: func(credential *sdk.Credential) (map[string]any, error) {
				value, _ := credential.Get("access")
				return map[string]any{"apiKey": value}, nil
			},
		}
		if method.LoginLabel != "" {
			oauth.LoginLabel = &method.LoginLabel
		}
		if method.Subscription {
			oauth.IsSubscription = &method.Subscription
		}
		auth.OAuth = oauth
	}
	return &sdk.Provider{
		ID: provider.ID, Name: provider.Name, Auth: auth, Stream: unused, StreamSimple: unused,
		GetModels: func() ([]map[string]any, error) { return []map[string]any{model}, nil },
	}
}

// recorded counts the EventRecordCounted lines this process wrote.
var recorded atomic.Int64

// mark records that a tool or command started, for the harness to wait on.
func mark(name string) error { return os.WriteFile(".started-"+name, nil, 0o600) }

func registerTool(extension *sdk.Extension, tool toolSpec) {
	schema := sdk.Schema{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}}
	if tool.Schema != "" {
		var custom any
		if err := json.Unmarshal([]byte(tool.Schema), &custom); err != nil {
			panic(err)
		}
		schema, _ = custom.(map[string]any)
	}
	extension.Tool(tool.Name, tool.Description, schema, func(ctx sdk.Context, params map[string]any) (any, error) {
		switch tool.Behavior {
		case "echo":
			text, _ := params["text"].(string)
			return "echo:" + text, nil
		case "fail":
			return nil, errors.New("boom")
		case "crash":
			panic("tool crash")
		case "details":
			return sdk.ToolResult{Content: "with details", Details: map[string]any{"count": 2, "nested": map[string]any{"a": []any{1, 2}}}}, nil
		case "soft-error":
			return sdk.ToolResult{Content: "soft failure", IsError: true}, nil
		case "bad-text":
			return sdk.ToolResult{Content: "a\xed\xa0\xbdb"}, nil
		case "empty":
			return map[string]any{"content": []any{}}, nil
		case "blocks":
			return map[string]any{"content": []any{map[string]any{"type": "text", "text": "a"}, map[string]any{"type": "text", "text": "b"}}}, nil
		case "cooperative":
			if err := mark(tool.Name); err != nil {
				return nil, err
			}
			<-ctx.Done()
			return "cooperative stopped", nil
		case "hang":
			if err := mark(tool.Name); err != nil {
				return nil, err
			}
			select {}
		}
		return nil, errors.New("unknown tool behavior " + tool.Behavior)
	})
}

// pick projects the fields of an object the scenario compares.
func pick(object map[string]any, keys ...string) map[string]any {
	picked := map[string]any{}
	for _, key := range keys {
		picked[key] = object[key]
	}
	return picked
}

func query(ctx sdk.Context, name string) (any, error) {
	switch name {
	case "idle":
		return ctx.IsIdle()
	case "pending":
		return ctx.HasPendingMessages()
	case "system-prompt":
		return ctx.GetSystemPrompt()
	case "thinking":
		return ctx.GetThinkingLevel()
	case "all-tools":
		tools, err := ctx.GetAllTools()
		picked := []map[string]any{}
		for _, tool := range tools {
			picked = append(picked, map[string]any{"name": tool.Name, "description": tool.Description})
		}
		return picked, err
	case "session-header":
		header, err := ctx.SessionManager().GetHeader()
		return pick(header, "type", "id", "cwd"), err
	case "leaf-entry":
		entry, err := ctx.SessionManager().GetLeafEntry()
		return pick(entry, "type", "id"), err
	case "session-manager-id":
		return ctx.SessionManager().GetSessionID()
	}
	return nil, errors.New("unknown query " + name)
}

func registryQuery(ctx sdk.Context, arg string) (any, error) {
	query, provider, _ := strings.Cut(arg, ":")
	registry := ctx.ModelRegistry()
	model := map[string]any{"provider": provider, "id": "m"}
	switch query {
	case "has-auth":
		return registry.HasConfiguredAuth(model)
	case "using-oauth":
		return registry.IsUsingOAuth(model)
	case "provider-name":
		found, err := registry.GetProvider(provider)
		if found == nil {
			return nil, err
		}
		return found.Name, err
	}
	return nil, errors.New("unknown registry query " + query)
}

func action(ctx sdk.Context, name string) error {
	switch name {
	case "abort":
		ctx.Abort()
	case "shutdown":
		ctx.Shutdown()
	case "compact":
		ctx.Compact(map[string]any{"customInstructions": "be brief"})
	default:
		return errors.New("unknown action " + name)
	}
	return nil
}

var footerSubscribed sync.Once

func footerData(ctx sdk.Context, path string) error {
	data := ctx.FooterData()
	footerSubscribed.Do(func() {
		data.OnBranchChange(func() {
			branch, _ := data.GitBranch()
			appendLine(".branch-changes", branch)
		})
		data.OnBranchChange(func() { appendLine(".branch-changes", "unsubscribed-callback-ran") })()
	})
	branch, ok := data.GitBranch()
	var branchJSON any
	if ok {
		branchJSON = branch
	}
	body, err := json.Marshal(map[string]any{"branch": branchJSON, "statuses": data.ExtensionStatuses(), "count": data.AvailableProviderCount()})
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o600)
}

func appendLine(path, line string) {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		panic(err)
	}
	defer func() { _ = file.Close() }()
	_, _ = file.WriteString(line + "\n")
}

func lateRegister(extension *sdk.Extension, kind string) {
	switch kind {
	case "command":
		extension.Command("late-command", "Registered late", func(_ sdk.Context, args string) error { return os.WriteFile(args, []byte("ran late"), 0o600) })
	case "shortcut":
		extension.Shortcut("ctrl+alt+l", "Registered late", func(sdk.Context) error { return os.WriteFile(".shortcut-fired", []byte("late"), 0o600) })
	case "flag":
		extension.Flag("late-flag", sdk.FlagOptions{Description: "Registered late", Type: sdk.FlagString, Default: "late"})
	case "message-renderer":
		extension.MessageRenderer("late-type", func(sdk.Context, map[string]any, sdk.MessageRenderOptions, int) ([]string, error) { return nil, nil })
	case "entry-renderer":
		extension.EntryRenderer("late-type", func(sdk.Context, map[string]any, sdk.EntryRenderOptions, int) ([]string, error) { return nil, nil })
	default:
		panic("unknown late registration " + kind)
	}
}

func registerCommand(extension *sdk.Extension, command commandSpec) {
	extension.Command(command.Name, command.Description, func(ctx sdk.Context, path string) error {
		switch command.Op {
		case "noop":
			return nil
		case "error":
			return errors.New("command boom")
		case "crash":
			panic("command crash")
		case "ping":
			return os.WriteFile(path, []byte("pong"), 0o600)
		case "echo":
			return os.WriteFile(".echo-args", []byte(path), 0o600)
		case "footer-data":
			return footerData(ctx, path)
		case "late-register":
			lateRegister(extension, command.Arg)
			return os.WriteFile(path, []byte("registered"), 0o600)
		case "registered-provider":
			provider, err := ctx.ModelRegistry().GetRegisteredNativeProvider(command.Arg)
			if err != nil {
				return err
			}
			name := "none"
			if provider != nil {
				name = provider.Name
			}
			return os.WriteFile(path, []byte(name), 0o600)
		case "flag":
			value, err := ctx.GetFlag(command.Arg)
			if err != nil {
				return err
			}
			body, err := json.Marshal(value)
			if err != nil {
				return err
			}
			return os.WriteFile(path, body, 0o600)
		case "hang":
			if err := mark(command.Name); err != nil {
				return err
			}
			select {}
		case "signal-wait":
			if err := mark(command.Name); err != nil {
				return err
			}
			<-ctx.Signal().Done()
			return os.WriteFile(path, []byte("signal aborted while running"), 0o600)
		case "signal":
			signal := ctx.Signal()
			report := "live"
			if signal == nil {
				report = "none"
			} else if signal.Err() != nil {
				report = "aborted"
			}
			return os.WriteFile(path, []byte(report), 0o600)
		case "query":
			value, err := query(ctx, command.Arg)
			if err != nil {
				return err
			}
			body, err := json.Marshal(value)
			if err != nil {
				return err
			}
			return os.WriteFile(path, body, 0o600)
		case "registry":
			value, err := registryQuery(ctx, command.Arg)
			if err != nil {
				return err
			}
			body, err := json.Marshal(value)
			if err != nil {
				return err
			}
			return os.WriteFile(path, body, 0o600)
		case "action":
			if err := action(ctx, command.Arg); err != nil {
				return err
			}
			return os.WriteFile(path, []byte("called"), 0o600)
		case "reload-stale":
			if err := ctx.Reload(); err != nil {
				return err
			}
			return os.WriteFile(path, []byte(cwdProbe(ctx)), 0o600)
		}
		return errors.New("unknown command op " + command.Op)
	})
}

// Extension is the conventional Go factory the host builds and runs.
func Extension() *sdk.Extension {
	body, err := os.ReadFile(".sdk-scenario.json")
	if err != nil {
		panic(err)
	}
	var scenario spec
	if err := json.Unmarshal(body, &scenario); err != nil {
		panic(err)
	}
	extension := sdk.New("scenario-driver-go")
	for _, provider := range scenario.Providers {
		if err := extension.RegisterNativeProvider(providerObject(provider)); err != nil {
			panic(err)
		}
	}
	for _, tool := range scenario.Tools {
		registerTool(extension, tool)
	}
	for _, command := range scenario.Commands {
		registerCommand(extension, command)
	}
	for _, shortcut := range scenario.Shortcuts {
		extension.Shortcut(shortcut.Key, shortcut.Description, func(sdk.Context) error {
			return os.WriteFile(".shortcut-fired", []byte(shortcut.Key), 0o600)
		})
	}
	for _, event := range scenario.Events {
		extension.OnEvent(event.Name, func(_ sdk.Context, data map[string]any) (any, error) {
			switch event.Op {
			case "throw":
				return nil, errors.New("event boom")
			case "crash":
				panic("event crash")
			case "block":
				return map[string]any{"block": true, "reason": "event blocked"}, nil
			case "set-content":
				return map[string]any{"content": []any{map[string]any{"type": "text", "text": "patched"}}}, nil
			case "set-error":
				return map[string]any{"isError": true}, nil
			case "record-counted":
				body, err := json.Marshal(data)
				if err != nil {
					return nil, err
				}
				appendLine(".events", fmt.Sprintf("%s@%d:%s", event.Tag, recorded.Add(1), body))
				return nil, nil
			case "record":
				body, err := json.Marshal(data)
				if err != nil {
					return nil, err
				}
				appendLine(".events", event.Tag+":"+string(body))
				return nil, nil
			case "set-prompt":
				return map[string]any{"systemPrompt": "base prompt"}, nil
			case "append-prompt":
				prompt, err := data["systemPrompt"].(*sdk.SystemPrompt).Get()
				if err != nil {
					return nil, err
				}
				return map[string]any{"systemPrompt": prompt + "+"}, nil
			case "note":
				return map[string]any{"message": map[string]any{"customType": "note", "content": "hello", "display": true}}, nil
			case "see-prompt":
				return map[string]any{"message": map[string]any{"customType": "saw", "content": data["prompt"], "display": false}}, nil
			case "transform":
				text, _ := data["text"].(string)
				return map[string]any{"action": "transform", "text": text + "!"}, nil
			case "handled":
				return map[string]any{"action": "handled"}, nil
			case "continue":
				return map[string]any{"action": "continue"}, nil
			case "see-content":
				content, _ := data["content"].([]any)
				var saw any
				if len(content) > 0 {
					block, _ := content[0].(map[string]any)
					saw = block["text"]
				}
				return map[string]any{"details": map[string]any{"saw": saw}}, nil
			}
			return nil, nil
		})
	}
	for _, flag := range scenario.Flags {
		extension.Flag(flag.Name, sdk.FlagOptions{Description: flag.Description, Type: sdk.FlagType(flag.Type), Default: flag.Default})
	}
	return extension
}

// cwdProbe reads ctx.Cwd and reports whether it answered or threw the stale message.
func cwdProbe(ctx sdk.Context) (report string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			report = fmt.Sprintf("cwd threw: %v", recovered)
		}
	}()
	_ = ctx.Cwd()
	return "cwd live"
}
