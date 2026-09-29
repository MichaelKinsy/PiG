# Native replacement and invocation API freeze

This document freezes the native signatures used by the replacement and prompt port helpers. It does not claim complete mode factory wiring, SDK callback transport, resource-loader integration or custom-message boundary closure.

```go
type PromptOptions struct {
    ExpandPromptTemplates *bool
    Images                []ai.ImageContent
    StreamingBehavior     extension.DeliverAs
    Source                extension.InputSource
    PreflightResult       func(bool)
}

func (*Session) Prompt(context.Context, string, ...*PromptOptions) ([]agent.AgentMessage, error)
func (*Session) SendUserMessage(context.Context, any, *extension.SendUserMessageOptions) error
func (*Session) SendExtensionUserMessage(any, *extension.SendUserMessageOptions) error
func (*Session) SendCustomMessage(context.Context, extension.CustomMessageRef, *extension.SendMessageOptions) error
func (*Session) SendMessage(extension.CustomMessageRef, *extension.SendMessageOptions) error
```

`extension.SendUserMessageOptions` contains `DeliverAs extension.DeliverAs` and `ExpandPromptTemplates *bool`. `extension.ReplacedSessionSendUserMessageOptions` aliases that type. `extension.SendMessageOptions.TriggerTurn` is `*bool` so omission differs from explicit false. These are invocation options, not `BuildSystemPromptOptions` or SDK event-object representations.

Direct `SendUserMessage` and `SendCustomMessage` await the Session operation. `SendExtensionUserMessage` and `SendMessage` are the bound extension actions: the Session owns their continuations and reports errors. `Prompt` keeps the stable two-result Go return shape. Default prompt expansion is true; default direct/bound user-message expansion is false. `SetPromptResources` accepts the resource owner's resolved template and skill collections; it does not discover or configure resources.

The Runtime seam remains:

```go
type CreateAgentSessionRuntimeFactory func(context.Context, CreateAgentSessionRuntimeOptions) (CreateAgentSessionRuntimeResult, error)
func CreateAgentSessionRuntime(context.Context, CreateAgentSessionRuntimeFactory, CreateAgentSessionRuntimeOptions) (*Runtime, error)
func (*Runtime) SetRebindSession(func(context.Context, *Session) error)
```

`NewSessionOptions.Setup` receives the actual SessionManager. `NewSessionOptions.WithSession`, `ForkOptions.WithSession` and `SwitchSessionOptions.WithSession` receive the fresh `*extension.ReplacedSessionContext`. Runtime awaits setup, context refresh, mode rebinding and the callback in that order. `CreateAgentSessionRuntimeResult.Dispose func(reason string)` requests per-result retirement once after logical disposal; final physical Host draining remains the HostOwner's responsibility.

The shared native test factory is `newRuntimeTestHarness` in `coding/session_runtime_replacement_upstream_test.go`. It constructs real Services, SessionManager, Session and Runtime values. External-package helpers should supply their own factory callback to the same production `CreateAgentSessionRuntime`, not add another runtime implementation. The original #2860 native cases live in `coding/session_replaced_2860_upstream_test.go` and the strict Pi pair is Session27.

## Qualification boundary

The native #2860 cases, awaited user-message guard, retirement order/idempotence, literal/template invocation and custom persistence-before-events tests pass. Full agent, coding and internal-codingagent packages pass. Native/Windows vet, package lint and focused race3 pass. Session27 passes three strict pairs. Four compiling mutations fail callback, retirement, custom-persistence and default-literal guards; callback omission also fails the paired scenario.

This freezes the interface before further runtime qualification. Resource collection and every-mode resource publication, cross-SDK option propagation, all pending-custom/settled reentrancy combinations and production factory replacement remain separate work. HostOwner, callback handles, logical scopes and physical cleanup remain Extension Host-owned. No transport version or compatibility reader is introduced.

Sources: Pi0.87.1 `agent-session.ts:264-275,1606-1774,1934-2035,3020-3065,4000-4007` and `agent-session-runtime.ts:164-193,230-259,261-350`. Source-only adaptation also retains the reviewed constructor correction from `7a1ce7a2638c1a58ecb775b4b2d3c315160bf790`; normalized builder/getter production remains parent-owned.
