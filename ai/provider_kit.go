package ai

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
)

type AssistantStreamBuilder = assistantStreamBuilder

type BodyReadOwner = bodyReadOwner

type BodyReadSignal = bodyReadSignal

type ObservedBodyReadiness = observedBodyReadiness

func BorrowedCurrentTools[M RoleMessage](messages TranscriptMessages[M]) []ToolSchema {
	return borrowedCurrentTools[M](messages)
}

func CollapsedMessages(context TranscriptContext) []Message {
	return collapsedMessages(context)
}

func ContextTransportError(ctx context.Context) error {
	return contextTransportError(ctx)
}

type ContinuationExecutor = continuationExecutor

type ContinuationTurn = continuationTurn

func FirstNonEmptyString(vals ...string) string {
	return firstNonEmptyString(vals...)
}

func GetJSONSchemaToolParameters(tool ToolSchema, strict *bool) (jsonSchemaToolParameters, error) {
	return getJSONSchemaToolParameters(tool, strict)
}

func GetProviderEnvValue(name string, env ProviderEnv) string {
	return getProviderEnvValue(name, env)
}

func HttpBodyReadOwner(conn net.Conn) *bodyReadOwner {
	return httpBodyReadOwner(conn)
}

func IsGoTransportError(err error) bool {
	return isGoTransportError(err)
}

type JsAsyncGenerator[T any] = jsAsyncGenerator[T]

func JsAwait[T any](awaiter jsAwaiter, operand jsOperand[T]) (T, error) {
	return jsAwait[T](awaiter, operand)
}

func JsPromiseOperand[T any](promise *jsPromise[T]) jsOperand[T] {
	return jsPromiseOperand[T](promise)
}

func MarshalInRecordedOrder(value any, order schemaObjectOrder) ([]byte, error) {
	return marshalInRecordedOrder(value, order)
}

func MarshalSchemaWithOrder(value any, order schemaObjectOrder, path string) ([]byte, error) {
	return marshalSchemaWithOrder(value, order, path)
}

func MarshalToolParameters(parameters map[string]any, order schemaObjectOrder) ([]byte, error) {
	return marshalToolParameters(parameters, order)
}

func MergeProviderEnv(configured ProviderEnv, request ProviderEnv) ProviderEnv {
	return mergeProviderEnv(configured, request)
}

func ModelProviderID(model *Model) string {
	return modelProviderID(model)
}

func NewDiagnosticDetails() diagnosticDetails {
	return newDiagnosticDetails()
}

func NewIdleTimeoutConn(conn net.Conn, network string) *idleTimeoutConn {
	return newIdleTimeoutConn(conn, network)
}

func NewObservedBodyReadiness(body *observedResponseBody) *observedBodyReadiness {
	return newObservedBodyReadiness(body)
}

func NewObservedProviderBuilder(ctx context.Context, api API, provider string, model string) *assistantStreamBuilder {
	return newObservedProviderBuilder(ctx, api, provider, model)
}

func NodeNetErrorMessage(err error) (string, bool) {
	return nodeNetErrorMessage(err)
}

type NodeReadable = nodeReadable

type NodeTransportError = nodeTransportError

func NormalizeJSONValue(value any) (any, error) {
	return normalizeJSONValue(value)
}

func ObserveProviderResponse(ctx context.Context, opts StreamOptions, response *http.Response, model *Model) error {
	return observeProviderResponse(ctx, opts, response, model)
}

type ObservedResponseBody = observedResponseBody

func ProviderEventModel(selected *Model, api API, providerID string, id string) *Model {
	return providerEventModel(selected, api, providerID, id)
}

func ResolveJSONSchemaStrictSampling(tool ToolSchema, supportsStrictMode bool, isUnsupportedKeyword UnsupportedStrictSchemaKeywordCheck) (*bool, error) {
	return resolveJSONSchemaStrictSampling(tool, supportsStrictMode, isUnsupportedKeyword)
}

func SanitizeSurrogates(s string) string {
	return sanitizeSurrogates(s)
}

type SchemaObjectOrder = schemaObjectOrder

type StreamToolCallDelta = streamToolCallDelta

func SuspendContinuation(turn *continuationTurn) {
	suspendContinuation(turn)
}

func ToolChoiceName(choice any) (string, bool) {
	return toolChoiceName(choice)
}

func TrimJSWhitespace(value string) string {
	return trimJSWhitespace(value)
}

func Utf16Length(value string) int {
	return utf16Length(value)
}

func ValidateProviderRequest(ctx context.Context, transcript TranscriptContext) error {
	return validateProviderRequest(ctx, transcript)
}

func BorrowedWithoutInitialSystemMessage(messages []Message) []Message {
	return withoutInitialSystemMessage(messages)
}

func (r ToolCall) ArgumentsValue() any {
	return r.argumentsValue()
}

func (r TranscriptContext) WithRepairedToolFlow() TranscriptContext {
	return r.withRepairedToolFlow()
}

func (r *assistantStreamBuilder) CalculateCost(usage *Usage) {
	r.calculateCost(usage)
}

func (r *assistantStreamBuilder) Done(reason StopReason, usage *Usage, errorMessage string) {
	r.done(reason, usage, errorMessage)
}

func (r *assistantStreamBuilder) EndToolCall(index int) {
	r.endToolCall(index)
}

func (r *assistantStreamBuilder) Fail(reason StopReason, err error) {
	r.fail(reason, err)
}

func (r *assistantStreamBuilder) ObserveProviderEventData(data any) error {
	return r.observeProviderEventData(data)
}

func (r *assistantStreamBuilder) Publish() *AssistantMessage {
	return r.publish()
}

func (r *assistantStreamBuilder) ResponseTurn(body func() error) error {
	return r.responseTurn(body)
}

func (r *assistantStreamBuilder) RunResponse(run func() error) {
	r.runResponse(run)
}

func (r *assistantStreamBuilder) SetProviderEventObserver(opts StreamOptions, model *Model) {
	r.setProviderEventObserver(opts, model)
}

func (r *assistantStreamBuilder) SetResponseMetadata(responseID string, responseModel string, rawStopReason string, providerThinkingLevel string, endTurn *bool) {
	r.setResponseMetadata(responseID, responseModel, rawStopReason, providerThinkingLevel, endTurn)
}

func (r *assistantStreamBuilder) Start() {
	r.start()
}

func (r *assistantStreamBuilder) TextBlockDelta(contentIndex int, delta string) {
	r.textBlockDelta(contentIndex, delta)
}

func (r *assistantStreamBuilder) TextBlockEnd(contentIndex int, content string, signature string) {
	r.textBlockEnd(contentIndex, content, signature)
}

func (r *assistantStreamBuilder) TextBlockStart() int {
	return r.textBlockStart()
}

func (r *assistantStreamBuilder) ThinkingBlockDelta(contentIndex int, delta string) {
	r.thinkingBlockDelta(contentIndex, delta)
}

func (r *assistantStreamBuilder) ThinkingBlockEnd(contentIndex int, content string, signature string) {
	r.thinkingBlockEnd(contentIndex, content, signature)
}

func (r *assistantStreamBuilder) ThinkingBlockStartWithContent(content ThinkingContent) int {
	return r.thinkingBlockStartWithContent(content)
}

func (r *assistantStreamBuilder) ToolCallDelta(delta streamToolCallDelta) {
	r.toolCallDelta(delta)
}

func (r *assistantStreamBuilder) ToolCallStart(delta streamToolCallDelta) {
	r.toolCallStart(delta)
}

func (r *observedBodyReadiness) Begin(deliver func(bodyReadSignal)) (bodyReadSignal, bool) {
	return r.begin(deliver)
}

func (r *continuationExecutor) PostExternal(reaction func()) {
	r.postExternal(reaction)
}

func (r diagnosticDetails) Attach(diagnostic AssistantMessageDiagnostic) AssistantMessageDiagnostic {
	return r.attach(diagnostic)
}

func (r diagnosticDetails) Set(key string, value any, order json.RawMessage) {
	r.set(key, value, order)
}

func (r *jsAsyncGenerator[T]) Dispose() {
	r.dispose()
}

func (r *jsAsyncGenerator[T]) Next() *jsPromise[jsIterResult[T]] {
	return r.next()
}

func (r *nodeReadable) AsyncIterator() *jsAsyncGenerator[[]byte] {
	return r.asyncIterator()
}

func (r *nodeReadable) ErrorOrDestroy(err error) {
	r.errorOrDestroy(err)
}

func (r *nodeReadable) Push(chunk []byte, eof bool) bool {
	return r.push(chunk, eof)
}

func (r *observedResponseBody) Retire() {
	r.retire()
}

func (r *streamedBlocks) AppendSignature(contentIndex int, current string, delta string) string {
	return r.appendSignature(contentIndex, current, delta)
}

// The accessors below read and write the unexported fields of the kit types: a provider package builds its stream
// through them in the order the JavaScript source runs.

// Stream is the event stream the builder fills.
func (r *assistantStreamBuilder) Stream() *AssistantMessageEventStream { return r.stream }

// Partial is the message the builder has built so far. A provider reads and edits it in place before it publishes.
func (r *assistantStreamBuilder) Partial() *AssistantMessage { return r.partial }

// Turn is the continuation turn the response runs in, valid inside ResponseTurn.
func (r *assistantStreamBuilder) Turn() *continuationTurn { return r.turn }

// SetModelCost sets the prices the builder's usage cost uses (StreamOptions.ModelCost).
func (r *assistantStreamBuilder) SetModelCost(cost ModelCost) { r.modelCost = cost }

// SetAbort sets the function that cancels the request when the stream is aborted.
func (r *assistantStreamBuilder) SetAbort(abort context.CancelFunc) { r.abort = abort }

// SetManaged marks the response as read by the provider itself, so the builder does not drive it.
func (r *assistantStreamBuilder) SetManaged(managed bool) { r.managed = managed }

// ClearToolCalls forgets the streamed tool calls.
func (r *assistantStreamBuilder) ClearToolCalls() { clear(r.toolCalls) }

// NewStreamToolCallDelta is the delta that starts or continues the tool call at index.
func NewStreamToolCallDelta(index int, id, name, argumentsDelta string) StreamToolCallDelta {
	return streamToolCallDelta{index: index, id: id, name: name, argumentsDelta: argumentsDelta}
}

// RawThinking reports whether the provider sends the thinking options as given, as the API's own stream does, instead of through the simple stream's levels and budgets.
func (o StreamOptions) RawThinking() bool { return o.rawThinking }

// SignatureEmpty reports whether the thinking block carries an empty signature a provider streamed, as opposed to none.
func (c ThinkingContent) SignatureEmpty() bool { return c.thinkingSignatureEmpty }

// WithSignatureEmpty returns c marked as carrying an empty signature the provider streamed.
func (c ThinkingContent) WithSignatureEmpty(empty bool) ThinkingContent {
	c.thinkingSignatureEmpty = empty
	return c
}

// SetSplitsAfterCost records that the provider created CacheWrite1h or Reasoning after the other members (Usage.MarshalJSON).
func (u *Usage) SetSplitsAfterCost(splits bool) { u.splitsAfterCost = splits }

// NonObjectArguments is the JSON text of arguments that are valid JSON but not an object; Arguments is nil while it is set.
func (c ToolCall) NonObjectArguments() json.RawMessage { return c.nonObjectArguments }

// ArgumentOrder is the member order the arguments were received in.
func (c ToolCall) ArgumentOrder() SchemaObjectOrder { return c.argumentOrder }

// Order is the member order of the schema's objects.
func (p jsonSchemaToolParameters) Order() SchemaObjectOrder { return p.order }

// Empty reports whether no diagnostic value was set.
func (d diagnosticDetails) Empty() bool { return len(d.values) == 0 }

// NewNodeTransportError is the transport error with Node's message, wrapping cause.
func NewNodeTransportError(message string, cause error) *NodeTransportError {
	return &nodeTransportError{message: message, cause: cause}
}

// NewObservedResponseBody wraps body so the provider reads it as a continuation turn does, through the connection owner.
func NewObservedResponseBody(body io.ReadCloser, owner *BodyReadOwner) *ObservedResponseBody {
	return &observedResponseBody{ReadCloser: body, owner: owner}
}

// Data is the bytes a read delivered.
func (s bodyReadSignal) Data() []byte { return s.data }

// Err is the error a read ended with, io.EOF at the end of the body.
func (s bodyReadSignal) Err() error { return s.err }

// Executor is the executor the turn runs on.
func (t *continuationTurn) Executor() *continuationExecutor { return t.executor }

// NewIncomingMessageReadable is the Readable of a Node http.IncomingMessage.
func NewIncomingMessageReadable(executor *continuationExecutor) *NodeReadable {
	return newNodeReadable(executor, nodeReadableOptions{incomingMessage: true})
}

// Done reports whether the iterator completed.
func (r jsIterResult[T]) Done() bool { return r.done }

// Value is the item the iterator yielded.
func (r jsIterResult[T]) Value() T { return r.value }

// ThinkingBindingControlsBeta is the Anthropic beta that binds replayed signed thinking blocks to the system prompt and tools they were created with.
const ThinkingBindingControlsBeta = thinkingBindingControlsBeta

// Kit entries the provider packages' tests need: they drive the executor and read the ticks the way the Node oracle records them.

func AwaitContinuation[T any](turn *continuationTurn, promise *continuationPromise[T]) T {
	return awaitContinuation[T](turn, promise)
}

type ContinuationTurnKey = continuationTurnKey

func EventPartial(event AssistantMessageEvent) *AssistantMessage {
	return eventPartial(event)
}

func NewAssistantStreamBuilder(ctx context.Context, api API, provider string, model string) *assistantStreamBuilder {
	return newAssistantStreamBuilder(ctx, api, provider, model)
}

func WithContinuationExecutor(ctx context.Context) (context.Context, *continuationExecutor) {
	return withContinuationExecutor(ctx)
}

func (r *AssistantMessageEventStream) ResultContinuation(executor *continuationExecutor) *continuationPromise[*AssistantMessage] {
	return r.resultContinuation(executor)
}

func (r *continuationExecutor) Run(body func(*continuationTurn)) {
	r.run(body)
}

// SetTrace makes the executor call external before each external completion (a Node event-loop macrotask) and microtask before each ready reaction (a Node microtask job).
func (r *continuationExecutor) SetTrace(external, microtask func()) {
	r.trace = &executorTrace{external: external, microtask: microtask}
}

// NewProviderError is an error that carries the status and body of the response it reports, as an SDK error does.
func NewProviderError(message string, status *int, body any) error {
	return &providerError{message: message, status: status, body: body}
}
