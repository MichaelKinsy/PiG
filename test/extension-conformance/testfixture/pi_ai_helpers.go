package testfixture

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// registerPiAiHelpersFixture adds pi_ai_helpers_probe: it runs every call of the vector calls (test/parity/sdk-vectors, Pi's own answers at the pin, listed in one file by the test)
// through this SDK's pi-ai helpers and reports each answer as JSON (TestConformance_PiAiHelpersAgreeAcrossSDKs).
func registerPiAiHelpersFixture(ext *sdk.Extension) {
	ext.Command("pi_ai_helpers_probe", "Run the golden pi-ai helper calls", func(ctx sdk.Context, args string) error {
		content, err := os.ReadFile(args)
		if err != nil {
			return err
		}
		var golden struct {
			Cases []struct {
				Fn   string            `json:"fn"`
				Args []json.RawMessage `json:"args"`
			} `json:"cases"`
			Timing []struct {
				Kind      string   `json:"kind"`
				Timestamp string   `json:"timestamp"`
				Preset    *float64 `json:"preset"`
			} `json:"timing"`
			Auth struct {
				Cases []struct {
					Name       string            `json:"name"`
					EnvVars    []string          `json:"envVars"`
					Credential json.RawMessage   `json:"credential"`
					Env        map[string]string `json:"env"`
				} `json:"cases"`
			} `json:"auth"`
		}
		if err := json.Unmarshal(content, &golden); err != nil {
			return err
		}
		cases := make([][]any, len(golden.Cases))
		for i, c := range golden.Cases {
			value, defined, err := piAiHelperCall(c.Fn, c.Args)
			if err != nil {
				return fmt.Errorf("%s: %w", c.Fn, err)
			}
			cases[i] = []any{}
			if defined {
				cases[i] = []any{value}
			}
		}
		auth := make([]any, len(golden.Auth.Cases))
		for i, c := range golden.Auth.Cases {
			var credential *sdk.Credential
			if len(c.Credential) > 0 && string(c.Credential) != "null" {
				credential = &sdk.Credential{}
				if err := json.Unmarshal(c.Credential, credential); err != nil {
					return err
				}
			}
			var seen []string
			result, err := sdk.EnvAPIKeyAuth(c.Name, c.EnvVars).Resolve(sdk.APIKeyAuthInput{Credential: credential, Signal: context.Background(), Ctx: sdk.AuthContext{Env: func(name string) (*string, error) {
				seen = append(seen, name)
				if value, ok := c.Env[name]; ok {
					return &value, nil
				}
				return nil, nil
			}}})
			if err != nil {
				return err
			}
			auth[i] = map[string]any{"result": result, "seen": seen}
		}
		var prompts []map[string]any
		credential, err := sdk.EnvAPIKeyAuth("Name", []string{"FIRST", "SECOND"}).Login(sdk.AuthInteraction{Signal: context.Background(), Prompt: func(request map[string]any) (string, error) {
			prompts = append(prompts, request)
			return "typed-key", nil
		}})
		if err != nil {
			return err
		}
		timing := make([]any, len(golden.Timing))
		for i, c := range golden.Timing {
			stream := sdk.CreateAssistantMessageEventStream()
			message := func(now bool) map[string]any {
				m := map[string]any{"role": "assistant", "content": []any{}, "stopReason": "stop"}
				if c.Kind == "error" {
					m["stopReason"] = "error"
				}
				switch {
				case now || c.Timestamp == "now":
					m["timestamp"] = float64(time.Now().UnixMilli())
				case c.Timestamp == "old":
					m["timestamp"] = float64(1)
				}
				if c.Preset != nil && !now {
					m["durationMs"] = *c.Preset
				}
				return m
			}
			final, later := message(false), message(true)
			switch c.Kind {
			case "done":
				stream.Push(map[string]any{"type": "done", "reason": "stop", "message": final})
			case "error":
				stream.Push(map[string]any{"type": "error", "reason": "error", "error": final})
			case "end":
				stream.End(final)
			default:
				stream.Push(map[string]any{"type": "done", "reason": "stop", "message": final})
				stream.End(later)
			}
			duration, present := final["durationMs"]
			value := any(nil)
			if c.Preset != nil && duration == *c.Preset {
				value = *c.Preset
			}
			_, laterTimed := later["durationMs"]
			timing[i] = map[string]any{"present": present, "value": value, "laterTimed": laterTimed}
		}
		wire, err := json.Marshal(map[string]any{"cases": cases, "timing": timing, "auth": auth, "login": map[string]any{"prompts": prompts, "result": credential}})
		if err != nil {
			return err
		}
		ctx.Notify("pi_ai_helpers:"+string(wire)+":end", "info")
		return nil
	})
}

func piAiHelperCall(fn string, args []json.RawMessage) (value any, defined bool, err error) {
	var (
		messages []json.RawMessage
		object   map[string]any
		flag     bool
		number   float64
		text     string
	)
	decode := func(i int, into any) {
		if err == nil {
			err = json.Unmarshal(args[i], into)
		}
	}
	optionalRaw := func(raw json.RawMessage, rawErr error) (any, bool, error) {
		if rawErr != nil || raw == nil {
			return nil, false, rawErr
		}
		return raw, true, nil
	}
	switch fn {
	case "StringEnum":
		var values []string
		decode(0, &values)
		var options *sdk.StringEnumOptions
		if len(args) > 1 {
			var given struct{ Description, Default string }
			decode(1, &given)
			options = &sdk.StringEnumOptions{Description: given.Description, Default: given.Default}
		}
		return sdk.StringEnum(values, options), true, err
	case "getSupportedThinkingLevels":
		decode(0, &object)
		return sdk.GetSupportedThinkingLevels(object), true, err
	case "clampThinkingLevel":
		decode(0, &object)
		decode(1, &text)
		return sdk.ClampThinkingLevel(object, text), true, err
	case "calculateCost":
		var usage map[string]any
		decode(0, &object)
		decode(1, &usage)
		if err != nil {
			return nil, false, err
		}
		cost, calcErr := sdk.CalculateCost(object, usage)
		return cost, true, calcErr
	case "contentText":
		var content any
		decode(0, &content)
		var separator []string
		if len(args) > 1 {
			decode(1, &text)
			separator = []string{text}
		}
		if err != nil {
			return nil, false, err
		}
		result, textErr := sdk.ContentText(content, separator...)
		return result, true, textErr
	case "getCurrentTools", "getDeclaredTools", "withoutInitialSystemMessage":
		decode(0, &messages)
		if err != nil {
			return nil, false, err
		}
		var tools []json.RawMessage
		switch fn {
		case "getCurrentTools":
			tools, err = sdk.GetCurrentTools(messages)
		case "getDeclaredTools":
			tools, err = sdk.GetDeclaredTools(messages)
		default:
			tools, err = sdk.WithoutInitialSystemMessage(messages)
		}
		return tools, true, err
	case "getCurrentSystemMessage", "getInitialSystemMessage":
		decode(0, &messages)
		if err != nil {
			return nil, false, err
		}
		if fn == "getInitialSystemMessage" {
			return optionalRaw(sdk.GetInitialSystemMessage(messages))
		}
		return optionalRaw(sdk.GetCurrentSystemMessage(messages))
	case "getCurrentSystemPrompt":
		decode(0, &messages)
		if err != nil {
			return nil, false, err
		}
		result, promptErr := sdk.GetCurrentSystemPrompt(messages)
		return result, true, promptErr
	case "hasNonAdditiveToolChanges":
		decode(0, &messages)
		if err != nil {
			return nil, false, err
		}
		result, changeErr := sdk.HasNonAdditiveToolChanges(messages)
		return result, true, changeErr
	case "resolveTranscriptTools":
		decode(0, &messages)
		decode(1, &text)
		if err != nil {
			return nil, false, err
		}
		result, resolveErr := sdk.ResolveTranscriptTools(messages, sdk.TranscriptToolSupport(text))
		return map[string]any{"requestTools": result.RequestTools, "foldedSystemMessages": result.FoldedSystemMessages}, true, resolveErr
	case "collapseSystemMessages":
		return optionalRaw(sdk.CollapseSystemMessages(args[0]))
	case "resolveTranscript":
		decode(1, &flag)
		return optionalRaw(sdk.ResolveTranscript(args[0], flag))
	case "normalizeContext":
		return optionalRaw(sdk.NormalizeContext(args[0]))
	case "createInitialSystemMessage":
		var prompt *string
		decode(0, &prompt)
		var tools []json.RawMessage
		if len(args) > 1 {
			decode(1, &tools)
		}
		if err != nil {
			return nil, false, err
		}
		return optionalRaw(sdk.CreateInitialSystemMessage(prompt, tools))
	case "getSystemMessageText":
		result, textErr := sdk.GetSystemMessageText(args[0])
		return result, true, textErr
	case "renderSystemMessageUpdate":
		result, textErr := sdk.RenderSystemMessageUpdate(args[0])
		return result, true, textErr
	case "getModelType":
		decode(0, &object)
		return sdk.GetModelType(object), true, err
	case "isModelType":
		decode(0, &object)
		decode(1, &text)
		return sdk.IsModelType(object, text), true, err
	case "hasApi":
		decode(0, &object)
		decode(1, &text)
		return sdk.HasApi(object, text), true, err
	case "modelsAreEqual":
		var other map[string]any
		decode(0, &object)
		decode(1, &other)
		return sdk.ModelsAreEqual(object, other), true, err
	case "isRetryableAssistantError":
		decode(0, &object)
		return sdk.IsRetryableAssistantError(object), true, err
	case "isContextOverflow":
		decode(0, &object)
		if len(args) > 1 {
			decode(1, &number)
		}
		return sdk.IsContextOverflow(object, number), true, err
	case "isRecoverableLength":
		decode(0, &object)
		decode(1, &number)
		return sdk.IsRecoverableLength(object, number), true, err
	case "retryDelayMs":
		var policy struct {
			BaseDelayMs     float64  `json:"baseDelayMs"`
			MaxAgentDelayMs *float64 `json:"maxAgentDelayMs"`
		}
		var attempt int
		decode(0, &policy)
		decode(1, &attempt)
		return sdk.RetryDelayMs(sdk.RetryPolicy{BaseDelayMs: policy.BaseDelayMs, MaxAgentDelayMs: policy.MaxAgentDelayMs}, attempt), true, err
	case "DEFAULT_MAX_AGENT_RETRY_DELAY_MS":
		return sdk.DefaultMaxAgentRetryDelayMs, true, nil
	}
	return nil, false, errors.New("no Go counterpart")
}

// registerHostPathsFixture adds host_paths_probe: getPackageDir() and getShellConfig() as this SDK answers them, for the default shell and for the existing and the missing
// custom shell path in args ("<existing>|<missing>") (TestConformance_HostPathsAgreeAcrossSDKs).
func registerHostPathsFixture(ext *sdk.Extension) {
	ext.Command("host_paths_probe", "Report getPackageDir and getShellConfig", func(ctx sdk.Context, args string) error {
		existing, missing, _ := strings.Cut(args, "|")
		shell := func(custom ...string) map[string]any {
			config, err := sdk.GetShellConfig(custom...)
			if err != nil {
				return map[string]any{"error": err.Error()}
			}
			report := map[string]any{"shell": config.Shell, "args": config.Args}
			if config.CommandTransport != "" {
				report["commandTransport"] = config.CommandTransport
			}
			return report
		}
		packageDir, err := sdk.PackageDir()
		if err != nil {
			return err
		}
		wire, err := json.Marshal(map[string]any{"packageDir": packageDir, "default": shell(), "custom": shell(existing), "missing": shell(missing)})
		if err != nil {
			return err
		}
		ctx.Notify("host_paths:"+string(wire)+":end", "info")
		return nil
	})
}

// registerHostHelpersFixture adds host_helpers_probe: generateDiffString, generateUnifiedPatch, sessionEntryToContextMessages, convertToPng, resizeImage,
// readStoredCredential and copyToClipboard as this SDK answers them, over the golden file's inputs (TestConformance_HostHelpersAgreeAcrossSDKs). args is
// "<golden>|<auth.json path>|<text to copy>".
func registerHostHelpersFixture(ext *sdk.Extension) {
	ext.Command("host_helpers_probe", "Run the host-served pi helpers over the golden inputs", func(ctx sdk.Context, args string) error {
		parts := strings.SplitN(args, "|", 3)
		content, err := os.ReadFile(parts[0])
		if err != nil {
			return err
		}
		var golden struct {
			Diffs []struct {
				Path         string `json:"path"`
				OldContent   string `json:"oldContent"`
				NewContent   string `json:"newContent"`
				ContextLines *int   `json:"contextLines"`
				PatchOnly    bool   `json:"patchOnly"`
			} `json:"diffs"`
			Entries []struct {
				Entry map[string]any `json:"entry"`
			} `json:"entries"`
			Images []struct {
				Op       string `json:"op"`
				Data     string `json:"data"`
				MimeType string `json:"mimeType"`
				Options  *struct {
					MaxWidth    int `json:"maxWidth"`
					MaxHeight   int `json:"maxHeight"`
					MaxBytes    int `json:"maxBytes"`
					JPEGQuality int `json:"jpegQuality"`
				} `json:"options"`
			} `json:"images"`
		}
		if err := json.Unmarshal(content, &golden); err != nil {
			return err
		}
		diffs := make([]any, len(golden.Diffs))
		for i, c := range golden.Diffs {
			lines := []int{}
			if c.ContextLines != nil {
				lines = append(lines, *c.ContextLines)
			}
			path := c.Path
			if path == "" {
				path = "src/a.ts"
			}
			patch, err := ctx.GenerateUnifiedPatch(path, c.OldContent, c.NewContent, lines...)
			if err != nil {
				return err
			}
			report := map[string]any{"patch": patch}
			if !c.PatchOnly {
				diff, err := ctx.GenerateDiffString(c.OldContent, c.NewContent, lines...)
				if err != nil {
					return err
				}
				report["diff"], report["firstChangedLine"] = diff.Diff, diff.FirstChangedLine
			}
			diffs[i] = report
		}
		entries := make([]any, len(golden.Entries))
		for i, c := range golden.Entries {
			messages, err := sdk.SessionEntryToContextMessages(c.Entry)
			if err != nil {
				return err
			}
			entries[i] = messages
		}
		images := make([]any, len(golden.Images))
		for i, c := range golden.Images {
			var meta map[string]any
			if c.Op == "convert" {
				converted, err := ctx.ConvertToPng(c.Data, c.MimeType)
				if err != nil {
					return err
				}
				if converted != nil {
					meta = map[string]any{"mimeType": converted.MimeType, "decodes": converted.Data != ""}
				}
			} else {
				var options *sdk.ImageResizeOptions
				if c.Options != nil {
					options = &sdk.ImageResizeOptions{MaxWidth: c.Options.MaxWidth, MaxHeight: c.Options.MaxHeight, MaxBytes: c.Options.MaxBytes, JPEGQuality: c.Options.JPEGQuality}
				}
				input, err := base64.StdEncoding.DecodeString(c.Data)
				if err != nil {
					return err
				}
				resized, err := ctx.ResizeImage(input, c.MimeType, options)
				if err != nil {
					return err
				}
				if resized != nil {
					meta = map[string]any{"mimeType": resized.MimeType, "originalWidth": resized.OriginalWidth, "originalHeight": resized.OriginalHeight, "width": resized.Width, "height": resized.Height, "wasResized": resized.WasResized, "decodes": resized.Data != ""}
				}
			}
			images[i] = meta
		}
		credential := func(provider string, path ...string) any {
			stored, err := sdk.ReadStoredCredential(provider, path...)
			if err != nil || stored == nil {
				return nil
			}
			return stored
		}
		missingFile := parts[1] + ".missing"
		creds := map[string]any{"explicit": credential("anthropic", parts[1]), "default": credential("anthropic"), "absentProvider": credential("nobody", parts[1]), "missingFile": credential("anthropic", missingFile), "notJSON": credential("anthropic", parts[1]+".bad")}
		if err := ctx.CopyToClipboard(parts[2]); err != nil {
			return err
		}
		wire, err := json.Marshal(map[string]any{"diffs": diffs, "entries": entries, "images": images, "creds": creds})
		if err != nil {
			return err
		}
		ctx.Notify("host_helpers:"+string(wire)+":end", "info")
		return nil
	})
}
