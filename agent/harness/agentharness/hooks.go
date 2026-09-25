package agentharness

import (
	"maps"
	"reflect"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
)

// This file ports packages/agent/src/harness/hooks.ts.

// HookErrorReporter reports one isolated hook failure (upstream
// HookErrorReporter). A returned error propagates out of the hook runner.
type HookErrorReporter func(ctx harness.Context, err error, hook HookName, lane string) error

type hookRegistration struct {
	id      *string
	handler func(ctx harness.Context, event any) (any, error)
}

// HookRegistry is the ordered harness hook registry and aggregate runner
// (upstream HookRegistry). Handlers of one hook run sequentially in
// registration order; registration and unregistration during a run affect
// only later runs.
type HookRegistry struct {
	mu            sync.Mutex
	registrations map[HookName][]*hookRegistration
	reportError   HookErrorReporter
	closedError   error
}

var _ Hooks = (*HookRegistry)(nil)

// NewHookRegistry creates an empty registry reporting failures to reportError.
func NewHookRegistry(reportError HookErrorReporter) *HookRegistry {
	return &HookRegistry{registrations: map[HookName][]*hookRegistration{}, reportError: reportError}
}

func (registry *HookRegistry) on(name HookName, handler func(ctx harness.Context, event any) (any, error), options HookOptions) (func(), error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.closedError != nil {
		return nil, registry.closedError
	}
	registration := &hookRegistration{handler: func(ctx harness.Context, event any) (result any, err error) {
		// A panicking handler is a thrown upstream error.
		defer func() {
			if recovered := recover(); recovered != nil {
				result, err = nil, panicError(recovered)
			}
		}()
		return handler(ctx, event)
	}}
	if options.ID != nil {
		id := *options.ID
		registration.id = &id
	}
	registry.registrations[name] = append(registry.registrations[name], registration)
	return func() {
		registry.mu.Lock()
		defer registry.mu.Unlock()
		current := registry.registrations[name]
		if index := slices.Index(current, registration); index != -1 {
			registry.registrations[name] = slices.Delete(slices.Clone(current), index, index+1)
		}
	}, nil
}

func onTyped[E any, R any](registry *HookRegistry, name HookName, handler HookHandler[E, R], options HookOptions) (func(), error) {
	return registry.on(name, func(ctx harness.Context, event any) (any, error) {
		return handler(ctx, event.(E))
	}, options)
}

// OnBeforeRun registers a before_run handler.
func (registry *HookRegistry) OnBeforeRun(handler HookHandler[BeforeRunEvent, *BeforeRunResult], options HookOptions) (func(), error) {
	return onTyped(registry, HookBeforeRun, handler, options)
}

// OnBeforeDrive registers a before_drive handler.
func (registry *HookRegistry) OnBeforeDrive(handler func(ctx harness.Context, event BeforeDriveEvent) error, options HookOptions) (func(), error) {
	return registry.on(HookBeforeDrive, func(ctx harness.Context, event any) (any, error) {
		return nil, handler(ctx, event.(BeforeDriveEvent))
	}, options)
}

// OnBeforeRunEnd registers a before_run_end handler.
func (registry *HookRegistry) OnBeforeRunEnd(handler HookHandler[BeforeRunEndEvent, *BeforeRunEndResult], options HookOptions) (func(), error) {
	return onTyped(registry, HookBeforeRunEnd, handler, options)
}

// OnTransformContext registers a transform_context handler.
func (registry *HookRegistry) OnTransformContext(handler HookHandler[TransformContextEvent, *TransformContextResult], options HookOptions) (func(), error) {
	return onTyped(registry, HookTransformContext, handler, options)
}

// OnBeforeRequest registers a before_request handler.
func (registry *HookRegistry) OnBeforeRequest(handler HookHandler[BeforeRequestEvent, *BeforeRequestResult], options HookOptions) (func(), error) {
	return onTyped(registry, HookBeforeRequest, handler, options)
}

// OnBeforePayload registers a before_payload handler.
func (registry *HookRegistry) OnBeforePayload(handler HookHandler[BeforePayloadEvent, *BeforePayloadResult], options HookOptions) (func(), error) {
	return onTyped(registry, HookBeforePayload, handler, options)
}

// OnAfterResponse registers an after_response handler.
func (registry *HookRegistry) OnAfterResponse(handler HookHandler[AfterResponseEvent, *AfterResponseResult], options HookOptions) (func(), error) {
	return onTyped(registry, HookAfterResponse, handler, options)
}

// OnBeforeTool registers a before_tool handler.
func (registry *HookRegistry) OnBeforeTool(handler HookHandler[BeforeToolEvent, *BeforeToolResult], options HookOptions) (func(), error) {
	return onTyped(registry, HookBeforeTool, handler, options)
}

// OnAfterTool registers an after_tool handler.
func (registry *HookRegistry) OnAfterTool(handler HookHandler[AfterToolEvent, *AfterToolResult], options HookOptions) (func(), error) {
	return onTyped(registry, HookAfterTool, handler, options)
}

// OnBeforeCompaction registers a before_compaction handler.
func (registry *HookRegistry) OnBeforeCompaction(handler HookHandler[BeforeCompactionEvent, *BeforeCompactionResult], options HookOptions) (func(), error) {
	return onTyped(registry, HookBeforeCompaction, handler, options)
}

// OnBeforeNavigation registers a before_navigation handler.
func (registry *HookRegistry) OnBeforeNavigation(handler HookHandler[BeforeNavigationEvent, *BeforeNavigationResult], options HookOptions) (func(), error) {
	return onTyped(registry, HookBeforeNavigation, handler, options)
}

// Has reports whether any handler is registered for name.
func (registry *HookRegistry) Has(name HookName) bool {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return len(registry.registrations[name]) != 0
}

// Close refuses later registrations and gated non-tool runs with the first
// supplied error.
func (registry *HookRegistry) Close(err error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.closedError == nil {
		registry.closedError = err
	}
}

func (registry *HookRegistry) closed() error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return registry.closedError
}

func (registry *HookRegistry) registrationsFor(name HookName) []*hookRegistration {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return slices.Clone(registry.registrations[name])
}

// admitted runs body after synchronously passing the effect gate with a
// context that is also cancelled by the gate signal (upstream runWithGate /
// runToolWithGate). An already-cancelled admitted context fails before any
// handler runs, with its abort reason (throwIfAborted). When checkClosed is
// set, a closed registry refuses the run after admission (upstream
// runAdmitted); tool runs do not check it, like upstream runToolWithGate.
func admitted[R any](registry *HookRegistry, ctx harness.Context, gate Gate, checkClosed bool, body func(ctx harness.Context) (R, error)) (R, error) {
	var result R
	err := gate.Admit(func() error {
		admittedContext := harness.WithAbortSignal(ctx, gate.Signal())
		if admittedContext.Err() != nil {
			return harness.AbortError(admittedContext)
		}
		if checkClosed {
			if err := registry.closed(); err != nil {
				return err
			}
		}
		var err error
		result, err = body(admittedContext)
		return err
	})
	return result, err
}

func (registry *HookRegistry) report(ctx harness.Context, err error, name HookName, lane string) error {
	return registry.reportError(ctx, err, name, lane)
}

// RunBeforeRun runs before_run: each handler sees the prompt extended by
// earlier injections; failures are reported and skipped. Nil means nothing
// was injected.
func (registry *HookRegistry) RunBeforeRun(ctx harness.Context, gate Gate, event BeforeRunEvent) (*BeforeRunResult, error) {
	return admitted(registry, ctx, gate, true, func(ctx harness.Context) (*BeforeRunResult, error) {
		return registry.beforeRun(ctx, event)
	})
}

func (registry *HookRegistry) beforeRun(ctx harness.Context, event BeforeRunEvent) (*BeforeRunResult, error) {
	prompt := event.Prompt
	var injectedMessages []agent.AgentMessage
	for _, registration := range registry.registrationsFor(HookBeforeRun) {
		invocation := event
		invocation.Prompt = prompt
		value, err := registration.handler(ctx, invocation)
		if err != nil {
			if reportErr := registry.report(ctx, err, HookBeforeRun, event.Lane); reportErr != nil {
				return nil, reportErr
			}
			continue
		}
		if result, _ := value.(*BeforeRunResult); result != nil && result.Messages != nil {
			injectedMessages = append(slices.Clone(injectedMessages), result.Messages...)
			prompt = append(slices.Clone(prompt), result.Messages...)
		}
	}
	if len(injectedMessages) == 0 {
		return nil, nil //nolint:nilnil // upstream returns undefined when nothing was injected.
	}
	return &BeforeRunResult{Messages: injectedMessages}, nil
}

// RunBeforeDrive runs before_drive fail-closed: the first failure is reported
// and returned, and later handlers do not run.
func (registry *HookRegistry) RunBeforeDrive(ctx harness.Context, gate Gate, event BeforeDriveEvent) error {
	_, err := admitted(registry, ctx, gate, true, func(ctx harness.Context) (struct{}, error) {
		for _, registration := range registry.registrationsFor(HookBeforeDrive) {
			if _, err := registration.handler(ctx, event); err != nil {
				if reportErr := registry.report(ctx, err, HookBeforeDrive, event.Lane); reportErr != nil {
					return struct{}{}, reportErr
				}
				return struct{}{}, err
			}
		}
		return struct{}{}, nil
	})
	return err
}

// RunBeforeRunEnd runs before_run_end: the last defined follow-up wins;
// failures are reported and skipped.
func (registry *HookRegistry) RunBeforeRunEnd(ctx harness.Context, gate Gate, event BeforeRunEndEvent) (*BeforeRunEndResult, error) {
	return admitted(registry, ctx, gate, true, func(ctx harness.Context) (*BeforeRunEndResult, error) {
		var followUp *string
		for _, registration := range registry.registrationsFor(HookBeforeRunEnd) {
			value, err := registration.handler(ctx, event)
			if err != nil {
				if reportErr := registry.report(ctx, err, HookBeforeRunEnd, event.Lane); reportErr != nil {
					return nil, reportErr
				}
				continue
			}
			if result, _ := value.(*BeforeRunEndResult); result != nil && result.FollowUp != nil {
				followUp = result.FollowUp
			}
		}
		if followUp == nil {
			return nil, nil //nolint:nilnil // upstream returns undefined without a follow-up.
		}
		return &BeforeRunEndResult{FollowUp: followUp}, nil
	})
}

// RunTransformContext runs transform_context, threading messages and system
// prompt through handlers. The result is always present.
func (registry *HookRegistry) RunTransformContext(ctx harness.Context, gate Gate, event TransformContextEvent) (*TransformContextResult, error) {
	return admitted(registry, ctx, gate, true, func(ctx harness.Context) (*TransformContextResult, error) {
		messages := event.Messages
		systemPrompt := event.SystemPrompt
		for _, registration := range registry.registrationsFor(HookTransformContext) {
			invocation := event
			invocation.Messages = messages
			invocation.SystemPrompt = systemPrompt
			value, err := registration.handler(ctx, invocation)
			if err != nil {
				if reportErr := registry.report(ctx, err, HookTransformContext, event.Lane); reportErr != nil {
					return nil, reportErr
				}
				continue
			}
			if result, _ := value.(*TransformContextResult); result != nil {
				if result.Messages != nil {
					messages = result.Messages
				}
				if result.SystemPrompt != nil {
					systemPrompt = *result.SystemPrompt
				}
			}
		}
		return &TransformContextResult{Messages: messages, SystemPrompt: &systemPrompt}, nil
	})
}

// RunBeforeRequest runs before_request, applying each patch in order. Nil
// means no handler returned a patch; otherwise the result is the net patch
// from the original options.
func (registry *HookRegistry) RunBeforeRequest(ctx harness.Context, gate Gate, event BeforeRequestEvent) (*BeforeRequestResult, error) {
	return admitted(registry, ctx, gate, true, func(ctx harness.Context) (*BeforeRequestResult, error) {
		streamOptions := event.StreamOptions
		changed := false
		for _, registration := range registry.registrationsFor(HookBeforeRequest) {
			invocation := event
			invocation.StreamOptions = streamOptions
			value, err := registration.handler(ctx, invocation)
			if err != nil {
				if reportErr := registry.report(ctx, err, HookBeforeRequest, event.Lane); reportErr != nil {
					return nil, reportErr
				}
				continue
			}
			if result, _ := value.(*BeforeRequestResult); result != nil && result.StreamOptions != nil {
				streamOptions = ApplyStreamOptionsPatch(streamOptions, *result.StreamOptions)
				changed = true
			}
		}
		if !changed {
			return nil, nil //nolint:nilnil // upstream returns undefined without a patch.
		}
		patch := createStreamOptionsPatch(event.StreamOptions, streamOptions)
		return &BeforeRequestResult{StreamOptions: &patch}, nil
	})
}

// RunBeforePayload runs before_payload, threading the payload. The result is
// always present.
func (registry *HookRegistry) RunBeforePayload(ctx harness.Context, gate Gate, event BeforePayloadEvent) (*BeforePayloadResult, error) {
	return admitted(registry, ctx, gate, true, func(ctx harness.Context) (*BeforePayloadResult, error) {
		payload := event.Payload
		for _, registration := range registry.registrationsFor(HookBeforePayload) {
			invocation := event
			invocation.Payload = payload
			value, err := registration.handler(ctx, invocation)
			if err != nil {
				if reportErr := registry.report(ctx, err, HookBeforePayload, event.Lane); reportErr != nil {
					return nil, reportErr
				}
				continue
			}
			if result, _ := value.(*BeforePayloadResult); result != nil {
				payload = result.Payload
			}
		}
		return &BeforePayloadResult{Payload: payload}, nil
	})
}

// RunAfterResponse runs after_response, threading the message. The result is
// always present.
func (registry *HookRegistry) RunAfterResponse(ctx harness.Context, gate Gate, event AfterResponseEvent) (*AfterResponseResult, error) {
	return admitted(registry, ctx, gate, true, func(ctx harness.Context) (*AfterResponseResult, error) {
		message := event.Message
		for _, registration := range registry.registrationsFor(HookAfterResponse) {
			invocation := event
			invocation.Message = message
			value, err := registration.handler(ctx, invocation)
			if err != nil {
				if reportErr := registry.report(ctx, err, HookAfterResponse, event.Lane); reportErr != nil {
					return nil, reportErr
				}
				continue
			}
			if result, _ := value.(*AfterResponseResult); result != nil && result.Message != nil {
				message = *result.Message
			}
		}
		return &AfterResponseResult{Message: &message}, nil
	})
}

// RunBeforeTool runs before_tool with one telemetry span per handler (upstream
// runToolWithGate). The first block, or the first failure (reported, then
// converted to a block with the error message), stops later handlers. The
// result is always present; Args is set only when a handler replaced them.
func (registry *HookRegistry) RunBeforeTool(ctx harness.Context, gate Gate, event BeforeToolEvent) (*BeforeToolResult, error) {
	return admitted(registry, ctx, gate, false, func(ctx harness.Context) (*BeforeToolResult, error) {
		return registry.beforeTool(ctx, event)
	})
}

func (registry *HookRegistry) beforeTool(ctx harness.Context, event BeforeToolEvent) (*BeforeToolResult, error) {
	args := event.Args
	var block *ToolBlock
	for _, registration := range registry.registrationsFor(HookBeforeTool) {
		invocation := event
		invocation.Args = args
		value, err := registry.invokeToolRegistration(ctx, HookBeforeTool, registration, invocation, event.HookScope)
		if err != nil {
			if reportErr := registry.report(ctx, err, HookBeforeTool, event.Lane); reportErr != nil {
				return nil, reportErr
			}
			block = &ToolBlock{Reason: err.Error()}
			break
		}
		if result, _ := value.(*BeforeToolResult); result != nil {
			if result.Args != nil {
				args = result.Args
			}
			if result.Block != nil {
				block = result.Block
				break
			}
		}
	}
	result := &BeforeToolResult{Block: block}
	if !sameMap(args, event.Args) {
		result.Args = args
	}
	return result, nil
}

// RunAfterTool runs after_tool with one telemetry span per handler. Each
// handler sees the result as patched by earlier handlers; failures are
// reported and skipped. Nil means no handler patched anything.
func (registry *HookRegistry) RunAfterTool(ctx harness.Context, gate Gate, event AfterToolEvent) (*AfterToolResult, error) {
	return admitted(registry, ctx, gate, false, func(ctx harness.Context) (*AfterToolResult, error) {
		return registry.afterTool(ctx, event)
	})
}

func (registry *HookRegistry) afterTool(ctx harness.Context, event AfterToolEvent) (*AfterToolResult, error) {
	current := event
	aggregate := AfterToolResult{}
	touched := false
	for _, registration := range registry.registrationsFor(HookAfterTool) {
		value, err := registry.invokeToolRegistration(ctx, HookAfterTool, registration, current, event.HookScope)
		if err != nil {
			if reportErr := registry.report(ctx, err, HookAfterTool, event.Lane); reportErr != nil {
				return nil, reportErr
			}
			continue
		}
		result, _ := value.(*AfterToolResult)
		if result == nil {
			continue
		}
		if result.Content != nil {
			aggregate.Content, current.Content, touched = result.Content, result.Content, true
		}
		if result.Details != nil || result.SetDetails {
			aggregate.Details, aggregate.SetDetails = result.Details, true
			current.Details, current.HasDetails, touched = result.Details, true, true
		}
		if result.IsError != nil {
			aggregate.IsError, current.IsError, touched = result.IsError, *result.IsError, true
		}
		if result.Usage != nil {
			aggregate.Usage, current.Usage, touched = result.Usage, result.Usage, true
		}
		if result.Terminate != nil {
			aggregate.Terminate, touched = result.Terminate, true
		}
	}
	if !touched {
		return nil, nil //nolint:nilnil // upstream returns undefined when nothing was patched.
	}
	return &aggregate, nil
}

// RunBeforeCompaction returns the first handler result that declines or
// supplies a compaction. A result doing both is reported and skipped;
// failures are reported and skipped. Nil means no handler decided.
func (registry *HookRegistry) RunBeforeCompaction(ctx harness.Context, gate Gate, event BeforeCompactionEvent) (*BeforeCompactionResult, error) {
	return admitted(registry, ctx, gate, true, func(ctx harness.Context) (*BeforeCompactionResult, error) {
		for _, registration := range registry.registrationsFor(HookBeforeCompaction) {
			value, err := registration.handler(ctx, event)
			if err != nil {
				if reportErr := registry.report(ctx, err, HookBeforeCompaction, event.Lane); reportErr != nil {
					return nil, reportErr
				}
				continue
			}
			result, _ := value.(*BeforeCompactionResult)
			if result == nil {
				continue
			}
			if result.Decline && result.Compaction != nil {
				if reportErr := registry.report(ctx, errBothDeclineAnd(HookBeforeCompaction, "compaction"), HookBeforeCompaction, event.Lane); reportErr != nil {
					return nil, reportErr
				}
				continue
			}
			if result.Decline || result.Compaction != nil {
				return result, nil
			}
		}
		return nil, nil //nolint:nilnil // upstream returns undefined when no handler decided.
	})
}

// RunBeforeNavigation returns the first handler result that declines or
// supplies a summary, with the same rules as RunBeforeCompaction.
func (registry *HookRegistry) RunBeforeNavigation(ctx harness.Context, gate Gate, event BeforeNavigationEvent) (*BeforeNavigationResult, error) {
	return admitted(registry, ctx, gate, true, func(ctx harness.Context) (*BeforeNavigationResult, error) {
		for _, registration := range registry.registrationsFor(HookBeforeNavigation) {
			value, err := registration.handler(ctx, event)
			if err != nil {
				if reportErr := registry.report(ctx, err, HookBeforeNavigation, event.Lane); reportErr != nil {
					return nil, reportErr
				}
				continue
			}
			result, _ := value.(*BeforeNavigationResult)
			if result == nil {
				continue
			}
			if result.Decline && result.Summary != nil {
				if reportErr := registry.report(ctx, errBothDeclineAnd(HookBeforeNavigation, "summary"), HookBeforeNavigation, event.Lane); reportErr != nil {
					return nil, reportErr
				}
				continue
			}
			if result.Decline || result.Summary != nil {
				return result, nil
			}
		}
		return nil, nil //nolint:nilnil // upstream returns undefined when no handler decided.
	})
}

type hookResultConflictError struct {
	hook  HookName
	field string
}

func (err *hookResultConflictError) Error() string {
	return string(err.hook) + " hook cannot return both decline and " + err.field
}

func errBothDeclineAnd(hook HookName, field string) error {
	return &hookResultConflictError{hook: hook, field: field}
}

// invokeToolRegistration runs one tool-hook handler inside a pi.harness.hook
// span whose context parents nested telemetry (upstream startHarnessSpan).
// Blocked before_tool results record outcome "blocked"; failures record
// outcome "failed" and an explicit error status without details.
func (registry *HookRegistry) invokeToolRegistration(ctx harness.Context, name HookName, registration *hookRegistration, event any, scope HookScope) (any, error) {
	attributes := harness.SpanAttributes{
		"pi.lane.name":    scope.Lane,
		"pi.operation.id": scope.RunID,
		"pi.hook.name":    string(name),
	}
	if registration.id != nil {
		attributes["pi.hook.registration_id"] = *registration.id
	}
	var value any
	err := harness.GetTelemetryContext(ctx).StartSpan(harness.SpanOptions{Name: "pi.harness.hook", Attributes: attributes}, func(span harness.TelemetrySpan) error {
		spanContext := harness.WithTelemetryContext(ctx, span)
		result, err := registration.handler(spanContext, event)
		if err != nil {
			span.SetAttributes(harness.SpanAttributes{"pi.hook.outcome": "failed"})
			span.SetStatus(harness.SpanStatus{Status: harness.SpanStatusCodeError})
			return err
		}
		outcome := "completed"
		if name == HookBeforeTool {
			if blocked, _ := result.(*BeforeToolResult); blocked != nil && blocked.Block != nil {
				outcome = "blocked"
			}
		}
		span.SetAttributes(harness.SpanAttributes{"pi.hook.outcome": outcome})
		value = result
		return nil
	})
	return value, err
}

// sameMap reports map identity, the Go analogue of upstream object identity.
func sameMap[K comparable, V any](left, right map[K]V) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return reflect.ValueOf(left).UnsafePointer() == reflect.ValueOf(right).UnsafePointer()
}

// ApplyStreamOptionsPatch applies a before_request patch to stream options
// (upstream applyStreamOptionsPatch). The base is not modified; patched
// header/metadata maps are fresh copies.
func ApplyStreamOptionsPatch(base harness.AgentHarnessStreamOptions, patch harness.AgentHarnessStreamOptionsPatch) harness.AgentHarnessStreamOptions {
	next := base
	if patch.Transport.Present {
		next.Transport = ""
		if patch.Transport.Value != nil {
			next.Transport = *patch.Transport.Value
		}
	}
	applyIntPatch(&next.TimeoutMs, patch.TimeoutMs)
	applyIntPatch(&next.MaxRetries, patch.MaxRetries)
	applyIntPatch(&next.MaxRetryDelayMs, patch.MaxRetryDelayMs)
	if patch.CacheRetention.Present {
		next.CacheRetention = ""
		if patch.CacheRetention.Value != nil {
			next.CacheRetention = *patch.CacheRetention.Value
		}
	}
	if patch.Deferred.Present {
		next.Deferred = nil
		if patch.Deferred.Value != nil {
			deferred := *patch.Deferred.Value
			next.Deferred = &deferred
		}
	}
	next.Headers = applyMapPatch(base.Headers, patch.Headers)
	next.Metadata = applyMapPatch(base.Metadata, patch.Metadata)
	return next
}

func applyIntPatch(target **int, patch harness.FieldPatch[int]) {
	if !patch.Present {
		return
	}
	*target = nil
	if patch.Value != nil {
		value := *patch.Value
		*target = &value
	}
}

func applyMapPatch[V any](base map[string]V, patch harness.MapPatch[V]) map[string]V {
	if !patch.Present {
		return base
	}
	if patch.Entries == nil {
		return nil
	}
	next := make(map[string]V, len(base)+len(patch.Entries))
	maps.Copy(next, base)
	for key, value := range patch.Entries {
		if value == nil {
			delete(next, key)
		} else {
			next[key] = *value
		}
	}
	return next
}

// createStreamOptionsPatch computes the net patch from base to value
// (upstream createStreamOptionsPatch). Scalar fields compare by value; maps
// compare by identity first, then per key.
func createStreamOptionsPatch(base, value harness.AgentHarnessStreamOptions) harness.AgentHarnessStreamOptionsPatch {
	patch := harness.AgentHarnessStreamOptionsPatch{}
	if base.Transport != value.Transport {
		patch.Transport = stringFieldPatch(value.Transport)
	}
	patch.TimeoutMs = intFieldPatch(base.TimeoutMs, value.TimeoutMs)
	patch.MaxRetries = intFieldPatch(base.MaxRetries, value.MaxRetries)
	patch.MaxRetryDelayMs = intFieldPatch(base.MaxRetryDelayMs, value.MaxRetryDelayMs)
	if base.CacheRetention != value.CacheRetention {
		patch.CacheRetention = stringFieldPatch(value.CacheRetention)
	}
	if base.Deferred != value.Deferred && (base.Deferred == nil || value.Deferred == nil || *base.Deferred != *value.Deferred) {
		patch.Deferred = harness.FieldPatch[harness.AgentHarnessDeferredOption]{Present: true, Value: value.Deferred}
	}
	patch.Headers = createMapPatch(base.Headers, value.Headers, func(a, b string) bool { return a == b })
	patch.Metadata = createMapPatch(base.Metadata, value.Metadata, sameJSONValue)
	return patch
}

func stringFieldPatch[T ~string](value T) harness.FieldPatch[T] {
	if value == "" {
		return harness.FieldPatch[T]{Present: true}
	}
	return harness.FieldPatch[T]{Present: true, Value: &value}
}

func intFieldPatch(base, value *int) harness.FieldPatch[int] {
	if base == nil && value == nil {
		return harness.FieldPatch[int]{}
	}
	if base != nil && value != nil && *base == *value {
		return harness.FieldPatch[int]{}
	}
	return harness.FieldPatch[int]{Present: true, Value: value}
}

func createMapPatch[V any](base, value map[string]V, equal func(a, b V) bool) harness.MapPatch[V] {
	if sameMap(base, value) {
		return harness.MapPatch[V]{}
	}
	if value == nil {
		return harness.MapPatch[V]{Present: true}
	}
	entries := map[string]*V{}
	for key := range base {
		if _, ok := value[key]; !ok {
			entries[key] = nil
		}
	}
	for key, entry := range value {
		if previous, ok := base[key]; !ok || !equal(previous, entry) {
			entry := entry
			entries[key] = &entry
		}
	}
	if base == nil && len(entries) == 0 {
		return harness.MapPatch[V]{Present: true, Entries: map[string]*V{}}
	}
	if len(entries) != 0 {
		return harness.MapPatch[V]{Present: true, Entries: entries}
	}
	return harness.MapPatch[V]{}
}

// sameJSONValue is the upstream `!==` comparison for metadata values:
// primitives compare by value, maps and slices by identity.
func sameJSONValue(left, right any) bool {
	leftValue, rightValue := reflect.ValueOf(left), reflect.ValueOf(right)
	if !leftValue.IsValid() || !rightValue.IsValid() {
		return leftValue.IsValid() == rightValue.IsValid()
	}
	if leftValue.Type() != rightValue.Type() {
		return false
	}
	switch leftValue.Kind() {
	case reflect.Map, reflect.Slice, reflect.Pointer, reflect.Func, reflect.Chan:
		return leftValue.UnsafePointer() == rightValue.UnsafePointer()
	}
	if !leftValue.Comparable() {
		return false
	}
	return leftValue.Equal(rightValue)
}
