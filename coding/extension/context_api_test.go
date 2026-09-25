package extension_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestContext_NonUISurfaceSignatures verifies the method signatures that map
// non-UI members of upstream ExtensionContext. It checks return arity and the
// error return required by Context's stale-runner guard.
func TestContext_NonUISurfaceSignatures(t *testing.T) {
	cases := []struct {
		upstream       string // upstream member name (camelCase)
		goName         string // expected Go method name
		wantNumOut     int
		errorIsLastOut bool
	}{
		{"sessionManager", "SessionManager", 2, true},         // upstream: types.ts:301
		{"modelRegistry", "ModelRegistry", 2, true},           // upstream: types.ts:303
		{"model", "Model", 2, true},                           // upstream: types.ts:305
		{"isIdle", "IsIdle", 2, true},                         // upstream: types.ts:307
		{"hasPendingMessages", "HasPendingMessages", 2, true}, // upstream: types.ts:313
		{"shutdown", "Shutdown", 1, true},                     // upstream: types.ts:315: void → just (error)
		{"getContextUsage", "GetContextUsage", 2, true},       // upstream: types.ts:317
		{"compact", "Compact", 1, true},                       // upstream: types.ts:319: void → just (error)
		{"abort", "Abort", 1, true},                           // upstream: types.ts:312: void → just (error)
	}

	ctxType := reflect.TypeFor[*extension.Context]()
	for _, tc := range cases {
		t.Run(tc.upstream+"→"+tc.goName, func(t *testing.T) {
			m, ok := ctxType.MethodByName(tc.goName)
			if !ok {
				t.Fatalf("extension.Context.%s missing for upstream member %q", tc.goName, tc.upstream)
			}
			// reflect.Method on pointer-receiver: .Type.NumOut()
			if got := m.Type.NumOut(); got != tc.wantNumOut {
				t.Errorf("Context.%s NumOut = %d, want %d "+
					"(consistent (value, error) contract: see assertActive guard)",
					tc.goName, got, tc.wantNumOut)
			}
			if tc.errorIsLastOut {
				lastOut := m.Type.Out(m.Type.NumOut() - 1)
				errIface := reflect.TypeFor[error]()
				if !lastOut.Implements(errIface) {
					t.Errorf("Context.%s last return = %v, want error "+
						"(every method must return an error per assertActive contract)",
						tc.goName, lastOut)
				}
			}
		})
	}
}

// TestContext_DefaultsMatchUpstream verifies the values returned before host
// actions are bound.
func TestContext_DefaultsMatchUpstream(t *testing.T) {
	c := extension.NewContext(".", nil, func() error { return nil }, extension.ContextActions{})

	t.Run("Model", func(t *testing.T) {
		got, err := c.Model()
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if got != nil {
			t.Errorf("Model() = %v, want nil (upstream stub: () => undefined)", got)
		}
	})

	t.Run("IsIdle", func(t *testing.T) {
		got, err := c.IsIdle()
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if !got {
			t.Errorf("IsIdle() = false, want true (upstream stub: () => true)")
		}
	})

	t.Run("HasPendingMessages", func(t *testing.T) {
		got, err := c.HasPendingMessages()
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if got {
			t.Errorf("HasPendingMessages() = true, want false (upstream stub: () => false)")
		}
	})

	t.Run("Shutdown_noPanic", func(t *testing.T) {
		if err := c.Shutdown(); err != nil {
			t.Errorf("Shutdown() err = %v, want nil (upstream stub is no-op)", err)
		}
	})

	t.Run("Abort_noPanic", func(t *testing.T) {
		if err := c.Abort(); err != nil {
			t.Errorf("Abort() err = %v, want nil (upstream stub is no-op)", err)
		}
	})

	t.Run("GetContextUsage", func(t *testing.T) {
		got, err := c.GetContextUsage()
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if got != nil {
			t.Errorf("GetContextUsage() = %v, want nil (upstream stub: () => undefined)", got)
		}
	})

	t.Run("Compact_noPanic", func(t *testing.T) {
		if err := c.Compact(nil); err != nil {
			t.Errorf("Compact(nil) err = %v, want nil (upstream stub is no-op)", err)
		}
		if err := c.Compact(&extension.CompactOptions{CustomInstructions: "x"}); err != nil {
			t.Errorf("Compact(opts) err = %v, want nil", err)
		}
	})
}

// TestContext_InjectedActionsForward verifies that Context delegates to every
// configured host action.
func TestContext_InjectedActionsForward(t *testing.T) {
	var (
		modelCalls   int
		idleCalls    int
		abortHit     bool
		shutdownHit  bool
		usageCalls   int
		compactCalls int
		compactArg   *extension.CompactOptions
	)

	customModel := struct{ name string }{name: "fake"}
	customUsage := &extension.ContextUsage{ContextWindow: 200000}

	actions := extension.ContextActions{
		GetModel:           func() extension.Model { modelCalls++; return customModel },
		IsIdle:             func() bool { idleCalls++; return false },
		HasPendingMessages: func() bool { return true },
		Abort:              func() { abortHit = true },
		Shutdown:           func() { shutdownHit = true },
		GetContextUsage:    func() *extension.ContextUsage { usageCalls++; return customUsage },
		GetSystemPrompt:    func() string { return "dynamic-sp" },
		Compact:            func(opts *extension.CompactOptions) { compactCalls++; compactArg = opts },
	}

	c := extension.NewContext(".", nil, func() error { return nil }, actions)

	if got, _ := c.Model(); got != customModel {
		t.Errorf("Model() = %v, want %v (injected GetModel not called)", got, customModel)
	}
	if got, _ := c.IsIdle(); got {
		t.Errorf("IsIdle() = true, want false (injected IsIdle not called)")
	}
	if got, _ := c.HasPendingMessages(); !got {
		t.Errorf("HasPendingMessages() = false, want true")
	}
	if err := c.Abort(); err != nil || !abortHit {
		t.Errorf("Abort: err=%v, hit=%v, want nil/true", err, abortHit)
	}
	if err := c.Shutdown(); err != nil || !shutdownHit {
		t.Errorf("Shutdown: err=%v, hit=%v, want nil/true", err, shutdownHit)
	}
	if got, _ := c.GetContextUsage(); got != customUsage {
		t.Errorf("GetContextUsage() = %v, want %v", got, customUsage)
	}
	if got, _ := c.GetSystemPrompt(); got != "dynamic-sp" {
		t.Errorf("GetSystemPrompt() = %q, want %q", got, "dynamic-sp")
	}
	wantCompact := &extension.CompactOptions{CustomInstructions: "summarize"}
	if err := c.Compact(wantCompact); err != nil {
		t.Errorf("Compact err = %v", err)
	}
	if compactArg != wantCompact {
		t.Errorf("Compact arg pointer not forwarded; got %v, want %v", compactArg, wantCompact)
	}

	// Counter sanity (each method called exactly once above).
	if modelCalls != 1 || idleCalls != 1 || usageCalls != 1 || compactCalls != 1 {
		t.Errorf("call counts: model=%d idle=%d usage=%d compact=%d, want all 1",
			modelCalls, idleCalls, usageCalls, compactCalls)
	}
}

// TestContext_StaleRunnerErrors verifies that each method checks the active
// runner before invoking a host action.
func TestContext_StaleRunnerErrors(t *testing.T) {
	staleErr := extension.ErrStaleContext
	c := extension.NewContext(".", nil, func() error { return staleErr }, extension.ContextActions{
		// Inject non-nil functions so the method must check
		// assertActive BEFORE invoking the action; if it skipped
		// the guard, these would run and the test would still pass
		// trivially. By panicking, the test fails LOUDLY if guard
		// is skipped.
		GetModel:           func() extension.Model { panic("must not call: stale guard skipped") },
		IsIdle:             func() bool { panic("must not call: stale guard skipped") },
		HasPendingMessages: func() bool { panic("must not call: stale guard skipped") },
		Abort:              func() { panic("must not call: stale guard skipped") },
		Shutdown:           func() { panic("must not call: stale guard skipped") },
		GetContextUsage:    func() *extension.ContextUsage { panic("must not call: stale guard skipped") },
		GetSystemPrompt:    func() string { panic("must not call: stale guard skipped") },
		Compact:            func(*extension.CompactOptions) { panic("must not call: stale guard skipped") },
	})

	checks := []struct {
		name string
		run  func() error
	}{
		{"SessionManager", func() error { _, err := c.SessionManager(); return err }},
		{"ModelRegistry", func() error { _, err := c.ModelRegistry(); return err }},
		{"Model", func() error { _, err := c.Model(); return err }},
		{"IsIdle", func() error { _, err := c.IsIdle(); return err }},
		{"HasPendingMessages", func() error { _, err := c.HasPendingMessages(); return err }},
		{"Abort", func() error { return c.Abort() }},
		{"Shutdown", func() error { return c.Shutdown() }},
		{"GetContextUsage", func() error { _, err := c.GetContextUsage(); return err }},
		{"GetSystemPrompt", func() error { _, err := c.GetSystemPrompt(); return err }},
		{"Compact", func() error { return c.Compact(nil) }},
	}

	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if err == nil {
				t.Fatalf("Context.%s on stale runner = nil err, want stale error", tc.name)
			}
			// Match the canonical sentinel; cross-package
			// equality lock per existing TestStaleError tests.
			if !errors.Is(err, extension.ErrStaleContext) {
				t.Errorf("Context.%s err = %v, want extension.ErrStaleContext", tc.name, err)
			}
		})
	}
}
