package extension

import (
	"context"
	"errors"
	"testing"
)

// runner.ts:383-386 and :566-630: the command context's newSession, fork, navigateTree and switchSession call the bound handler with their arguments; an unbound
// handler answers { cancelled: false }; a stale context answers { cancelled: true } with its error and never calls the handler; the context-aware handler
// wins over the plain one and receives the request's own context.
// mutation-checked: a missing stale check, a wrong unbound answer, a dropped argument, or the plain handler winning over the context-aware one fails.
func TestCommandContextSessionActionsFollowThePiBindingContract(t *testing.T) {
	type ctxKey struct{}
	stale := errors.New("stale runner")
	active := func() error { return nil }
	newCommand := func(assert func() error, actions CommandActions) *CommandContext {
		return NewCommandContext(NewContext("/w", nil, assert, ContextActions{}), actions)
	}
	type call struct {
		name string
		run  func(c *CommandContext) (CancelledResult, error)
		// plain and aware bind the handler under test and record what it received.
		bind func(actions *CommandActions, seen *string, cancelled bool, viaContext bool)
	}
	calls := []call{
		{"NewSession", func(c *CommandContext) (CancelledResult, error) {
			return c.NewSession(&NewSessionOptions{ParentSession: "parent"})
		},
			func(a *CommandActions, seen *string, cancelled, viaContext bool) {
				if viaContext {
					a.NewSessionContext = func(ctx context.Context, o *NewSessionOptions) (CancelledResult, error) {
						*seen = "ctx:" + ctx.Value(ctxKey{}).(string) + ":" + o.ParentSession
						return CancelledResult{Cancelled: cancelled}, nil
					}
					return
				}
				a.NewSession = func(o *NewSessionOptions) (CancelledResult, error) {
					*seen = "plain:" + o.ParentSession
					return CancelledResult{Cancelled: cancelled}, nil
				}
			}},
		{"Fork", func(c *CommandContext) (CancelledResult, error) { return c.Fork("entry-1", &ForkOptions{}) },
			func(a *CommandActions, seen *string, cancelled, viaContext bool) {
				if viaContext {
					a.ForkContext = func(ctx context.Context, id string, _ *ForkOptions) (CancelledResult, error) {
						*seen = "ctx:" + ctx.Value(ctxKey{}).(string) + ":" + id
						return CancelledResult{Cancelled: cancelled}, nil
					}
					return
				}
				a.Fork = func(id string, _ *ForkOptions) (CancelledResult, error) {
					*seen = "plain:" + id
					return CancelledResult{Cancelled: cancelled}, nil
				}
			}},
		{"NavigateTree", func(c *CommandContext) (CancelledResult, error) {
			return c.NavigateTree("target-1", &NavigateTreeOptions{})
		},
			func(a *CommandActions, seen *string, cancelled, viaContext bool) {
				if viaContext {
					a.NavigateTreeContext = func(ctx context.Context, id string, _ *NavigateTreeOptions) (CancelledResult, error) {
						*seen = "ctx:" + ctx.Value(ctxKey{}).(string) + ":" + id
						return CancelledResult{Cancelled: cancelled}, nil
					}
					return
				}
				a.NavigateTree = func(id string, _ *NavigateTreeOptions) (CancelledResult, error) {
					*seen = "plain:" + id
					return CancelledResult{Cancelled: cancelled}, nil
				}
			}},
		{"SwitchSession", func(c *CommandContext) (CancelledResult, error) {
			return c.SwitchSession("/s/one.jsonl", &SwitchSessionOptions{})
		},
			func(a *CommandActions, seen *string, cancelled, viaContext bool) {
				if viaContext {
					a.SwitchSessionContext = func(ctx context.Context, path string, _ *SwitchSessionOptions) (CancelledResult, error) {
						*seen = "ctx:" + ctx.Value(ctxKey{}).(string) + ":" + path
						return CancelledResult{Cancelled: cancelled}, nil
					}
					return
				}
				a.SwitchSession = func(path string, _ *SwitchSessionOptions) (CancelledResult, error) {
					*seen = "plain:" + path
					return CancelledResult{Cancelled: cancelled}, nil
				}
			}},
	}
	args := map[string]string{"NewSession": "parent", "Fork": "entry-1", "NavigateTree": "target-1", "SwitchSession": "/s/one.jsonl"}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			// Unbound: { cancelled: false }, no error.
			if got, err := tc.run(newCommand(active, CommandActions{})); err != nil || got.Cancelled {
				t.Fatalf("unbound = %+v, %v, want { cancelled: false }", got, err)
			}
			// Stale: { cancelled: true } with the error, handler not called.
			var seen string
			var actions CommandActions
			tc.bind(&actions, &seen, false, false)
			if got, err := tc.run(newCommand(func() error { return stale }, actions)); !errors.Is(err, stale) || !got.Cancelled || seen != "" {
				t.Fatalf("stale = %+v, %v, handler saw %q; want cancelled with the stale error and no call", got, err, seen)
			}
			// Plain handler: called with the arguments, its result returned.
			got, err := tc.run(newCommand(active, actions))
			if err != nil || got.Cancelled || seen != "plain:"+args[tc.name] {
				t.Fatalf("plain = %+v, %v, saw %q", got, err, seen)
			}
			tc.bind(&actions, &seen, true, false)
			if got, err := tc.run(newCommand(active, actions)); err != nil || !got.Cancelled {
				t.Fatalf("a handler's cancelled result was lost: %+v, %v", got, err)
			}
			// Context-aware handler wins and sees the request context.
			seen = ""
			tc.bind(&actions, &seen, true, true)
			command := newCommand(active, actions)
			WithCommandContext(context.WithValue(t.Context(), ctxKey{}, "req"), command)
			if got, err := tc.run(command); err != nil || !got.Cancelled || seen != "ctx:req:"+args[tc.name] {
				t.Fatalf("context-aware = %+v, %v, saw %q, want the request context and the arguments", got, err, seen)
			}
		})
	}
}
