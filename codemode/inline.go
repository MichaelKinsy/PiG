//go:build !tinygo

package codemode

// InlineSandboxOptions configures NewInlineSandbox.
//
// Ports packages/codemode/src/runtime/inline.ts (InlineCodemodeSandboxOptions). Wasm is optional here: nil means the embedded module.
type InlineSandboxOptions struct {
	SandboxOptions
	// InterruptBudget is the interrupt polls an execution may use before it fails with ErrorTimeout: the only limit on a
	// script that computes without awaiting a tool. Zero means DefaultInterruptBudget.
	InterruptBudget int
}

// InlineSandbox runs each execution's QuickJS VM under an interrupt budget, for hosts that cannot afford a script that
// computes forever: QuickJS polls its interrupt handler periodically while a script runs, and the execution fails with
// ErrorTimeout once the polls of the whole execution pass the budget, whatever the deadline says. The deadline and the
// context still end the execution whenever they fire.
//
// Like Pi's InlineCodemodeSandbox, the VM runs in-thread: on the goroutine that called Execute for the first segment of the
// script, then on whichever goroutine delivers a tool's result. The deadline is armed before the first segment, and the
// deadline, the context and Close take effect between turns, where a turn is a segment of the script and the delivery of the
// messages it queued: while the VM computes, only the budget can stop it, and a segment that computes past the deadline still
// delivers its result first. Whenever the script waits on a tool the deadline and the context apply.
//
// Ports packages/codemode/src/runtime/inline.ts (InlineCodemodeSandbox).
type InlineSandbox struct {
	*Sandbox
}

// NewInlineSandbox validates the globals and registers the tools, like NewSandbox.
func NewInlineSandbox(options InlineSandboxOptions) (*InlineSandbox, error) {
	budget := options.InterruptBudget
	if budget == 0 {
		budget = DefaultInterruptBudget
	}
	sandbox, err := newSandbox(options.SandboxOptions, func(s *Sandbox) vmLauncher {
		return inlineLauncher(s.engineFor, s.prelude, budget)
	})
	if err != nil {
		return nil, err
	}
	return &InlineSandbox{sandbox}, nil
}
