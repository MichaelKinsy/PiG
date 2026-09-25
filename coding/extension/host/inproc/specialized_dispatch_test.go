package inproc_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func extWithBeforeAgentStartHandler(path string, fn func(extension.BeforeAgentStartEvent, context.Context) *extension.BeforeAgentStartEventResult) extension.Extension {
	ext := newFakeExtension(path)
	ext.Handlers["before_agent_start"] = []extension.HandlerFn{
		func(args ...any) (any, error) {
			ev, _ := args[0].(extension.BeforeAgentStartEvent)
			ctx, _ := args[1].(context.Context)
			r := fn(ev, ctx)
			if r == nil {
				return nil, nil
			}
			return r, nil
		},
	}
	return ext
}

func extWithResourcesDiscoverHandler(path string, fn func(extension.ResourcesDiscoverEvent, context.Context) *extension.ResourcesDiscoverResult) extension.Extension {
	ext := newFakeExtension(path)
	ext.Handlers["resources_discover"] = []extension.HandlerFn{
		func(args ...any) (any, error) {
			ev, _ := args[0].(extension.ResourcesDiscoverEvent)
			ctx, _ := args[1].(context.Context)
			r := fn(ev, ctx)
			if r == nil {
				return nil, nil
			}
			return r, nil
		},
	}
	return ext
}

// ─── EmitBeforeAgentStart ─────────────────────────────────────────────────

func TestEmitBeforeAgentStart_StaleRunnerReturnsErrStaleContext(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	r.Invalidate("")
	_, err := r.EmitBeforeAgentStart(context.Background(), "p", nil, "sp", extension.BuildSystemPromptOptions{})
	if !errors.Is(err, extension.ErrStaleContext) {
		t.Errorf("err = %v, want ErrStaleContext", err)
	}
}

func TestEmitBeforeAgentStart_NoHandlersReturnsNil(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	got, err := r.EmitBeforeAgentStart(context.Background(), "p", nil, "sp", extension.BuildSystemPromptOptions{})
	if err != nil || got != nil {
		t.Errorf("got=(%v, %v), want (nil, nil)", got, err)
	}
}

func TestEmitBeforeAgentStart_TypedNilHandlerResultReturnsNil(t *testing.T) {
	ext := newFakeExtension("/ext/typed-nil")
	ext.Handlers["before_agent_start"] = []extension.HandlerFn{func(...any) (any, error) {
		return (*extension.BeforeAgentStartEventResult)(nil), nil
	}}
	r := inproc.NewRunner([]extension.Extension{ext}, ".")
	got, err := r.EmitBeforeAgentStart(context.Background(), "p", nil, "sp", extension.BuildSystemPromptOptions{})
	if err != nil || got != nil {
		t.Fatalf("got=(%v, %v), want (nil, nil)", got, err)
	}
}

func TestEmitBeforeAgentStart_NoOpHandlersReturnsNil(t *testing.T) {
	exts := []extension.Extension{
		extWithBeforeAgentStartHandler("/ext/a", func(extension.BeforeAgentStartEvent, context.Context) *extension.BeforeAgentStartEventResult {
			return nil
		}),
		extWithBeforeAgentStartHandler("/ext/b", func(extension.BeforeAgentStartEvent, context.Context) *extension.BeforeAgentStartEventResult {
			return &extension.BeforeAgentStartEventResult{} // empty fields
		}),
	}
	r := inproc.NewRunner(exts, ".")
	got, err := r.EmitBeforeAgentStart(context.Background(), "p", nil, "sp", extension.BuildSystemPromptOptions{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != nil {
		t.Errorf("got = %+v, want nil (no handler modified state)", got)
	}
}

// TestEmitBeforeAgentStart_MessagesAccumulate: every handler that
// returns Message pushes into the combined Messages slice in order.
func TestEmitBeforeAgentStart_MessagesAccumulate(t *testing.T) {
	exts := []extension.Extension{
		extWithBeforeAgentStartHandler("/ext/a", func(extension.BeforeAgentStartEvent, context.Context) *extension.BeforeAgentStartEventResult {
			return &extension.BeforeAgentStartEventResult{Message: &extension.CustomMessageRef{CustomType: "t-a"}}
		}),
		extWithBeforeAgentStartHandler("/ext/b", func(extension.BeforeAgentStartEvent, context.Context) *extension.BeforeAgentStartEventResult {
			return &extension.BeforeAgentStartEventResult{Message: &extension.CustomMessageRef{CustomType: "t-b"}}
		}),
	}
	r := inproc.NewRunner(exts, ".")
	got, err := r.EmitBeforeAgentStart(context.Background(), "p", nil, "sp", extension.BuildSystemPromptOptions{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got == nil {
		t.Fatal("got = nil, want messages")
		return
	}
	if len(got.Messages) != 2 {
		t.Fatalf("Messages len = %d, want 2", len(got.Messages))
	}
	if got.Messages[0].CustomType != "t-a" || got.Messages[1].CustomType != "t-b" {
		t.Errorf("Messages = %+v, want order [t-a, t-b]", got.Messages)
	}
	if got.SystemPrompt != nil {
		t.Errorf("SystemPrompt = %q, want nil (no handler modified it)", *got.SystemPrompt)
	}
}

// TestEmitBeforeAgentStart_SystemPromptChainCompounds: handler A
// rewrites systemPrompt, handler B sees A's rewrite via event.SystemPrompt
// and rewrites again. Final SystemPrompt is B's value.
//
// upstream: runner.ts:898-918 (currentSystemPrompt threads via the event)
func TestEmitBeforeAgentStart_SystemPromptChainCompounds(t *testing.T) {
	var bSawSP string
	exts := []extension.Extension{
		extWithBeforeAgentStartHandler("/ext/a", func(extension.BeforeAgentStartEvent, context.Context) *extension.BeforeAgentStartEventResult {
			return &extension.BeforeAgentStartEventResult{SystemPrompt: new("rewritten-by-a")}
		}),
		extWithBeforeAgentStartHandler("/ext/b", func(ev extension.BeforeAgentStartEvent, _ context.Context) *extension.BeforeAgentStartEventResult {
			bSawSP = ev.SystemPrompt
			return &extension.BeforeAgentStartEventResult{SystemPrompt: new("rewritten-by-b")}
		}),
	}
	r := inproc.NewRunner(exts, ".")
	got, err := r.EmitBeforeAgentStart(context.Background(), "p", nil, "original-sp", extension.BuildSystemPromptOptions{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got == nil {
		t.Fatal("got = nil")
		return
	}
	if got.SystemPrompt == nil || *got.SystemPrompt != "rewritten-by-b" {
		t.Errorf("SystemPrompt = %v, want rewritten-by-b", got.SystemPrompt)
	}
	if bSawSP != "rewritten-by-a" {
		t.Errorf("handler B saw SystemPrompt = %q, want rewritten-by-a (chain must compound)", bSawSP)
	}
}

// TestEmitBeforeAgentStart_GetSystemPromptTracksChainedValue proves the
// ExtensionContext getter reflects the latest mutated system prompt as the
// handler chain advances. This closes and mirrors upstream
// runner.ts:884-892 where createContext installs a dynamic getter inside
// EmitBeforeAgentStart.
func TestEmitBeforeAgentStart_GetSystemPromptTracksChainedValue(t *testing.T) {
	var aSaw, bSaw string
	exts := []extension.Extension{
		extWithBeforeAgentStartHandler("/ext/a", func(_ extension.BeforeAgentStartEvent, ctx context.Context) *extension.BeforeAgentStartEventResult {
			extCtx := extension.FromContext(ctx)
			if extCtx == nil {
				t.Fatal("extension context missing")
			}
			sp, err := extCtx.GetSystemPrompt()
			if err != nil {
				t.Fatalf("GetSystemPrompt() err = %v", err)
			}
			aSaw = sp
			return &extension.BeforeAgentStartEventResult{SystemPrompt: new("rewritten-by-a")}
		}),
		extWithBeforeAgentStartHandler("/ext/b", func(_ extension.BeforeAgentStartEvent, ctx context.Context) *extension.BeforeAgentStartEventResult {
			extCtx := extension.FromContext(ctx)
			if extCtx == nil {
				t.Fatal("extension context missing")
			}
			sp, err := extCtx.GetSystemPrompt()
			if err != nil {
				t.Fatalf("GetSystemPrompt() err = %v", err)
			}
			bSaw = sp
			return nil
		}),
	}
	r := inproc.NewRunner(exts, ".")
	if _, err := r.EmitBeforeAgentStart(context.Background(), "p", nil, "original-sp", extension.BuildSystemPromptOptions{}); err != nil {
		t.Fatalf("err = %v", err)
	}
	if aSaw != "original-sp" {
		t.Errorf("handler A GetSystemPrompt() = %q, want original-sp", aSaw)
	}
	if bSaw != "rewritten-by-a" {
		t.Errorf("handler B GetSystemPrompt() = %q, want rewritten-by-a", bSaw)
	}
}

func TestEmitBeforeAgentStart_HandlerErrorRoutesViaEmitErrorAndContinues(t *testing.T) {
	var subseq atomic.Int32
	exts := []extension.Extension{
		{
			Path:      "/ext/bad",
			Handlers:  map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) { return nil, errors.New("boom") }}},
			Tools:     map[string]extension.RegisteredTool{},
			Commands:  map[string]extension.RegisteredCommand{},
			Flags:     map[string]extension.ExtensionFlag{},
			Shortcuts: map[extension.KeyID]extension.ExtensionShortcut{},
		},
		extWithBeforeAgentStartHandler("/ext/good", func(extension.BeforeAgentStartEvent, context.Context) *extension.BeforeAgentStartEventResult {
			subseq.Add(1)
			return nil
		}),
	}
	r := inproc.NewRunner(exts, ".")
	var captured atomic.Int32
	r.AddErrorListener(func(e *extension.ExtensionError) {
		if e.Event == "before_agent_start" {
			captured.Add(1)
		}
	})
	if _, err := r.EmitBeforeAgentStart(context.Background(), "p", nil, "sp", extension.BuildSystemPromptOptions{}); err != nil {
		t.Fatalf("err = %v", err)
	}
	if captured.Load() != 1 {
		t.Errorf("captured %d, want 1", captured.Load())
	}
	if subseq.Load() != 1 {
		t.Errorf("subsequent fired %d, want 1", subseq.Load())
	}
}

// ─── EmitResourcesDiscover ────────────────────────────────────────────────

func TestEmitResourcesDiscover_StaleRunnerReturnsErrStaleContext(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	r.Invalidate("")
	_, err := r.EmitResourcesDiscover(context.Background(), ".", "startup")
	if !errors.Is(err, extension.ErrStaleContext) {
		t.Errorf("err = %v, want ErrStaleContext", err)
	}
}

// TestEmitResourcesDiscover_NoHandlersReturnsEmptyAggregate: no
// handlers ⇒ non-nil aggregate with empty slices (NOT nil slices, so
// callers can range without nil checks).
func TestEmitResourcesDiscover_NoHandlersReturnsEmptyAggregate(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	got, err := r.EmitResourcesDiscover(context.Background(), ".", "startup")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got == nil {
		t.Fatal("got = nil, want non-nil aggregate")
		return
	}
	if got.SkillPaths == nil || got.PromptPaths == nil || got.ThemePaths == nil {
		t.Errorf("aggregate has nil slices: %+v (want empty non-nil)", got)
	}
	if len(got.SkillPaths) != 0 || len(got.PromptPaths) != 0 || len(got.ThemePaths) != 0 {
		t.Errorf("non-empty result with no handlers: %+v", got)
	}
}

// TestEmitResourcesDiscover_AggregatesPathsWithExtensionAttribution:
// each path is paired with its source extension's path.
//
// upstream: runner.ts:962-974 (`{ path, extensionPath: ext.path }`)
func TestEmitResourcesDiscover_AggregatesPathsWithExtensionAttribution(t *testing.T) {
	exts := []extension.Extension{
		extWithResourcesDiscoverHandler("/ext/skills-pkg", func(extension.ResourcesDiscoverEvent, context.Context) *extension.ResourcesDiscoverResult {
			return &extension.ResourcesDiscoverResult{
				SkillPaths: []string{"/skills/a", "/skills/b"},
			}
		}),
		extWithResourcesDiscoverHandler("/ext/prompts-pkg", func(extension.ResourcesDiscoverEvent, context.Context) *extension.ResourcesDiscoverResult {
			return &extension.ResourcesDiscoverResult{
				PromptPaths: []string{"/prompts/x"},
			}
		}),
		extWithResourcesDiscoverHandler("/ext/multi-pkg", func(extension.ResourcesDiscoverEvent, context.Context) *extension.ResourcesDiscoverResult {
			return &extension.ResourcesDiscoverResult{
				SkillPaths: []string{"/skills/c"},
				ThemePaths: []string{"/themes/dark"},
			}
		}),
	}
	r := inproc.NewRunner(exts, ".")
	got, err := r.EmitResourcesDiscover(context.Background(), ".", "startup")
	if err != nil {
		t.Fatalf("err = %v", err)
	}

	// Skills: 3 from 2 different extensions.
	if len(got.SkillPaths) != 3 {
		t.Fatalf("SkillPaths len = %d, want 3", len(got.SkillPaths))
	}
	if got.SkillPaths[0].Path != "/skills/a" || got.SkillPaths[0].ExtensionPath != "/ext/skills-pkg" {
		t.Errorf("SkillPaths[0] = %+v, want {/skills/a, /ext/skills-pkg}", got.SkillPaths[0])
	}
	if got.SkillPaths[1].Path != "/skills/b" || got.SkillPaths[1].ExtensionPath != "/ext/skills-pkg" {
		t.Errorf("SkillPaths[1] = %+v", got.SkillPaths[1])
	}
	if got.SkillPaths[2].Path != "/skills/c" || got.SkillPaths[2].ExtensionPath != "/ext/multi-pkg" {
		t.Errorf("SkillPaths[2] = %+v, want {/skills/c, /ext/multi-pkg}", got.SkillPaths[2])
	}

	if len(got.PromptPaths) != 1 || got.PromptPaths[0].ExtensionPath != "/ext/prompts-pkg" {
		t.Errorf("PromptPaths = %+v", got.PromptPaths)
	}
	if len(got.ThemePaths) != 1 || got.ThemePaths[0].ExtensionPath != "/ext/multi-pkg" {
		t.Errorf("ThemePaths = %+v", got.ThemePaths)
	}
}

// TestEmitResourcesDiscover_EventCwdAndReasonPropagate: handler sees
// the cwd and reason passed to the runner.
func TestEmitResourcesDiscover_EventCwdAndReasonPropagate(t *testing.T) {
	var seenCwd, seenReason string
	exts := []extension.Extension{
		extWithResourcesDiscoverHandler("/ext/a", func(ev extension.ResourcesDiscoverEvent, _ context.Context) *extension.ResourcesDiscoverResult {
			seenCwd = ev.Cwd
			seenReason = ev.Reason
			return nil
		}),
	}
	r := inproc.NewRunner(exts, ".")
	_, _ = r.EmitResourcesDiscover(context.Background(), "/work/dir", "reload")
	if seenCwd != "/work/dir" {
		t.Errorf("Cwd = %q, want /work/dir", seenCwd)
	}
	if seenReason != "reload" {
		t.Errorf("Reason = %q, want reload", seenReason)
	}
}

func TestEmitResourcesDiscover_HandlerErrorRoutesViaEmitErrorAndContinues(t *testing.T) {
	var subseq atomic.Int32
	exts := []extension.Extension{
		{
			Path:      "/ext/bad",
			Handlers:  map[string][]extension.HandlerFn{"resources_discover": {func(args ...any) (any, error) { return nil, errors.New("boom") }}},
			Tools:     map[string]extension.RegisteredTool{},
			Commands:  map[string]extension.RegisteredCommand{},
			Flags:     map[string]extension.ExtensionFlag{},
			Shortcuts: map[extension.KeyID]extension.ExtensionShortcut{},
		},
		extWithResourcesDiscoverHandler("/ext/good", func(extension.ResourcesDiscoverEvent, context.Context) *extension.ResourcesDiscoverResult {
			subseq.Add(1)
			return nil
		}),
	}
	r := inproc.NewRunner(exts, ".")
	var captured atomic.Int32
	r.AddErrorListener(func(e *extension.ExtensionError) {
		if e.Event == "resources_discover" {
			captured.Add(1)
		}
	})
	if _, err := r.EmitResourcesDiscover(context.Background(), ".", "startup"); err != nil {
		t.Fatalf("err = %v", err)
	}
	if captured.Load() != 1 {
		t.Errorf("captured %d, want 1", captured.Load())
	}
	if subseq.Load() != 1 {
		t.Errorf("subsequent fired %d, want 1", subseq.Load())
	}
}

// TestEmitBeforeAgentStart_EmptySystemPromptReplaces: upstream applies
// `systemPrompt` whenever it is not undefined, so "" clears the prompt (EXT-23).
func TestEmitBeforeAgentStart_EmptySystemPromptReplaces(t *testing.T) {
	ext := newFakeExtension("/clear")
	ext.Handlers["before_agent_start"] = []extension.HandlerFn{func(...any) (any, error) {
		return json.RawMessage(`{"systemPrompt":""}`), nil
	}}
	r := inproc.NewRunner([]extension.Extension{ext}, ".")
	got, err := r.EmitBeforeAgentStart(context.Background(), "hi", nil, "original", extension.BuildSystemPromptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.SystemPrompt == nil || *got.SystemPrompt != "" {
		t.Fatalf("got = %+v, want an empty replacement system prompt", got)
	}
}
