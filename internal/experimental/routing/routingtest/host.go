// Package routingtest ports the server package's testing subpath (packages/server/src/testing): a scripted Session host, a protocol test client, and an unstarted test server for transport and routing conformance tests.
package routingtest

// Ports packages/server/src/testing/host.ts.
// Ports packages/server/src/testing/index.ts.

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
)

// Deferred is a one-shot value. The first Resolve wins; later calls are ignored, as with a settled Promise.
type Deferred[T any] struct {
	once  sync.Once
	done  chan struct{}
	value T
}

// NewDeferred returns an unresolved Deferred.
func NewDeferred[T any]() *Deferred[T] {
	return &Deferred[T]{done: make(chan struct{})}
}

// Promise closes when the Deferred resolves.
func (d *Deferred[T]) Promise() <-chan struct{} { return d.done }

// Resolve settles the Deferred with value unless it is already settled.
func (d *Deferred[T]) Resolve(value T) {
	d.once.Do(func() {
		d.value = value
		close(d.done)
	})
}

// Value returns the resolved value, or false while the Deferred is unresolved.
func (d *Deferred[T]) Value() (T, bool) {
	select {
	case <-d.done:
		return d.value, true
	default:
		var zero T
		return zero, false
	}
}

// OpenGate pauses one gated operation. Entered resolves when the operation reaches the gate; the operation continues after Release resolves. Upstream's gate type is unexported; Go exports it because exported methods return it.
type OpenGate struct {
	Entered *Deferred[struct{}]
	Release *Deferred[struct{}]
}

func newOpenGate() *OpenGate {
	return &OpenGate{Entered: NewDeferred[struct{}](), Release: NewDeferred[struct{}]()}
}

func (gate *OpenGate) pass() {
	gate.Entered.Resolve(struct{}{})
	<-gate.Release.Promise()
}

var okResult = json.RawMessage(`{"ok":true}`)

// TestHarness is a scripted routed Session handle. It records service calls, counts attachments, releases, and closes, and fails or gates the next operation on request. A nil service result is an omitted result, distinct from JSON null.
type TestHarness struct {
	metadata    routing.SessionMetadata
	closed      *Deferred[struct{}]
	termination *Deferred[error]

	mu                     sync.Mutex
	attachedClients        int
	attachmentReleaseCount int
	closeCount             int
	serviceCalls           []chord.ServiceCall
	failAttachmentRelease  error
	failClose              error
	nextServiceError       error
	nextServiceResult      json.RawMessage
	nextCloseGate          *OpenGate
	nextServiceGate        *OpenGate
}

// NewTestHarness returns a harness for metadata whose next service result is {"ok":true}.
func NewTestHarness(metadata routing.SessionMetadata) *TestHarness {
	return &TestHarness{metadata: metadata, closed: NewDeferred[struct{}](), termination: NewDeferred[error](), nextServiceResult: okResult}
}

// Metadata returns the Session metadata the harness was opened with.
func (h *TestHarness) Metadata() routing.SessionMetadata { return h.metadata }

// Closed resolves after a successful Close.
func (h *TestHarness) Closed() *Deferred[struct{}] { return h.closed }

// Terminated closes when the harness terminates: with nil after a successful Close, or with the error passed to Terminate.
func (h *TestHarness) Terminated() <-chan struct{} { return h.termination.Promise() }

// TerminalError returns the termination error, or nil after an expected close or before termination.
func (h *TestHarness) TerminalError() error {
	failure, _ := h.termination.Value()
	return failure
}

// AttachedClients returns the number of unreleased attachments.
func (h *TestHarness) AttachedClients() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.attachedClients
}

// AttachmentReleaseCount returns the number of release attempts on unreleased attachments, including failed attempts.
func (h *TestHarness) AttachmentReleaseCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.attachmentReleaseCount
}

// CloseCount returns the number of Close calls.
func (h *TestHarness) CloseCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closeCount
}

// ServiceCalls returns the service calls received so far, in arrival order.
func (h *TestHarness) ServiceCalls() []chord.ServiceCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.serviceCalls)
}

// FailAttachmentRelease returns the error every attachment release fails with, or nil.
func (h *TestHarness) FailAttachmentRelease() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.failAttachmentRelease
}

// SetFailAttachmentRelease makes every later attachment release fail with failure until it is set to nil.
func (h *TestHarness) SetFailAttachmentRelease(failure error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.failAttachmentRelease = failure
}

// FailClose returns the error the next Close fails with, or nil.
func (h *TestHarness) FailClose() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.failClose
}

// SetFailClose makes the next Close fail with failure.
func (h *TestHarness) SetFailClose(failure error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.failClose = failure
}

// NextServiceError returns the error the next service call fails with, or nil.
func (h *TestHarness) NextServiceError() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.nextServiceError
}

// SetNextServiceError makes the next service call fail with failure.
func (h *TestHarness) SetNextServiceError(failure error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextServiceError = failure
}

// NextServiceResult returns the result of the next successful service call.
func (h *TestHarness) NextServiceResult() json.RawMessage {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.nextServiceResult
}

// SetNextServiceResult sets the result of the next successful service call; nil is an omitted result. Later calls return {"ok":true} again.
func (h *TestHarness) SetNextServiceResult(result json.RawMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextServiceResult = result
}

// AttachClient opens one attachment. Its release is idempotent once it succeeds; a failed release leaves the attachment held.
func (h *TestHarness) AttachClient(context.Context) (routing.RoutedSessionAttachment, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.attachedClients++
	return &testHarnessAttachment{harness: h}, nil
}

type testHarnessAttachment struct {
	harness  *TestHarness
	released bool
}

func (a *testHarnessAttachment) InvokeService(_ context.Context, call chord.ServiceCall, _ chord.ServiceUpdatePublisher) (json.RawMessage, error) {
	return a.harness.InvokeService(call)
}

func (a *testHarnessAttachment) Release(context.Context) error {
	h := a.harness
	h.mu.Lock()
	defer h.mu.Unlock()
	if a.released {
		return nil
	}
	h.attachmentReleaseCount++
	if h.failAttachmentRelease != nil {
		return h.failAttachmentRelease
	}
	a.released = true
	h.attachedClients--
	return nil
}

// InvokeService records call, then fails with the next service error, or waits at the next service gate and returns the next service result.
func (h *TestHarness) InvokeService(call chord.ServiceCall) (json.RawMessage, error) {
	h.mu.Lock()
	h.serviceCalls = append(h.serviceCalls, call)
	if failure := h.nextServiceError; failure != nil {
		h.nextServiceError = nil
		h.mu.Unlock()
		return nil, failure
	}
	gate := h.nextServiceGate
	h.nextServiceGate = nil
	h.mu.Unlock()
	if gate != nil {
		gate.pass()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	result := h.nextServiceResult
	h.nextServiceResult = okResult
	return result, nil
}

// Close counts the close, waits at the next close gate, then fails with the next close error or resolves Closed and terminates with nil.
func (h *TestHarness) Close(context.Context) error {
	h.mu.Lock()
	h.closeCount++
	gate := h.nextCloseGate
	h.nextCloseGate = nil
	h.mu.Unlock()
	if gate != nil {
		gate.pass()
	}
	h.mu.Lock()
	failure := h.failClose
	h.failClose = nil
	h.mu.Unlock()
	if failure != nil {
		return failure
	}
	h.closed.Resolve(struct{}{})
	h.termination.Resolve(nil)
	return nil
}

// Terminate ends the harness unexpectedly with failure unless it has already terminated.
func (h *TestHarness) Terminate(failure error) {
	h.termination.Resolve(failure)
}

// GateNextClose pauses the next Close at the returned gate.
func (h *TestHarness) GateNextClose() *OpenGate {
	gate := newOpenGate()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextCloseGate = gate
	return gate
}

// GateNextServiceCall pauses the next service call at the returned gate.
func (h *TestHarness) GateNextServiceCall() *OpenGate {
	gate := newOpenGate()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextServiceGate = gate
	return gate
}

// CreateTestServerServices returns a server service host that supports only pi.session-management attach (one string argument) and detach (no arguments). Its attachments release nothing.
func CreateTestServerServices() routing.RoutedServerServiceHost {
	return testServerServices{}
}

type testServerServices struct{}

func (testServerServices) AttachClient(_ context.Context, presentation routing.RoutedServerPresentation) (routing.RoutedServerServiceAttachment, error) {
	return testServerAttachment{presentation: presentation}, nil
}

type testServerAttachment struct {
	presentation routing.RoutedServerPresentation
}

func (a testServerAttachment) InvokeService(ctx context.Context, call chord.ServiceCall, _ chord.ServiceUpdatePublisher) (json.RawMessage, error) {
	if call.Instance == nil && call.ServiceId == "pi.session-management" && call.Member == "attach" && len(call.Args) == 1 {
		if sessionID, ok := jsonString(call.Args[0]); ok {
			if err := a.presentation.AttachSession(ctx, sessionID); err != nil {
				return nil, err
			}
			return json.RawMessage("null"), nil
		}
	}
	if call.Instance == nil && call.ServiceId == "pi.session-management" && call.Member == "detach" && len(call.Args) == 0 {
		if err := a.presentation.DetachSession(ctx); err != nil {
			return nil, err
		}
		return json.RawMessage("null"), nil
	}
	return nil, errors.New("Unsupported test server service " + call.ServiceId + "." + call.Member)
}

func (testServerAttachment) Release(context.Context) error { return nil }

// jsonString decodes a JSON string value; any other JSON value, including null, is rejected.
func jsonString(value json.RawMessage) (string, bool) {
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return "", false
	}
	text, ok := decoded.(string)
	return text, ok
}

// TestServerHost is an in-memory Session host. Seeded Sessions resolve by ID, and each open creates a new TestHarness.
type TestServerHost struct {
	serverServices routing.RoutedServerServiceHost

	mu                    sync.Mutex
	sessions              map[string]routing.SessionMetadata
	harnesses             map[string][]*TestHarness
	openSessionCount      int
	nextOpenSessionError  error
	nextHarnessCloseError error
	nextOpenSessionGate   *OpenGate
}

// NewTestServerHost returns a host with no Sessions whose server services come from CreateTestServerServices.
func NewTestServerHost() *TestServerHost {
	return &TestServerHost{serverServices: CreateTestServerServices(), sessions: map[string]routing.SessionMetadata{}, harnesses: map[string][]*TestHarness{}}
}

// ServerHost returns the routing host backed by this test host. Upstream's TestServerHost implements ServerHost directly; Go's ServerHost is a struct of capabilities.
func (h *TestServerHost) ServerHost() routing.ServerHost {
	return routing.ServerHost{ServerServices: h.serverServices, ResolveSession: h.ResolveSession, OpenSession: h.OpenSession}
}

// ServerServices returns the host's server service endpoint.
func (h *TestServerHost) ServerServices() routing.RoutedServerServiceHost { return h.serverServices }

// Sessions returns a copy of the seeded Sessions by ID.
func (h *TestServerHost) Sessions() map[string]routing.SessionMetadata {
	h.mu.Lock()
	defer h.mu.Unlock()
	return maps.Clone(h.sessions)
}

// Harnesses returns a copy of the opened harnesses by Session ID, oldest first.
func (h *TestServerHost) Harnesses() map[string][]*TestHarness {
	h.mu.Lock()
	defer h.mu.Unlock()
	harnesses := make(map[string][]*TestHarness, len(h.harnesses))
	for id, opened := range h.harnesses {
		harnesses[id] = slices.Clone(opened)
	}
	return harnesses
}

// OpenSessionCount returns the number of OpenSession calls, including failed calls.
func (h *TestServerHost) OpenSessionCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.openSessionCount
}

// NextOpenSessionError returns the error the next open of a known Session fails with, or nil.
func (h *TestServerHost) NextOpenSessionError() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.nextOpenSessionError
}

// SetNextOpenSessionError makes the next open of a known Session fail with failure.
func (h *TestServerHost) SetNextOpenSessionError(failure error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextOpenSessionError = failure
}

// NextHarnessCloseError returns the close error given to the next opened harness, or nil.
func (h *TestServerHost) NextHarnessCloseError() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.nextHarnessCloseError
}

// SetNextHarnessCloseError makes the next opened harness fail its first Close with failure.
func (h *TestServerHost) SetNextHarnessCloseError(failure error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextHarnessCloseError = failure
}

// ResolveSession returns a seeded Session or a SessionNotFoundError.
func (h *TestServerHost) ResolveSession(_ context.Context, sessionID string) (routing.SessionMetadata, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	metadata, ok := h.sessions[sessionID]
	if !ok {
		return nil, routing.NewSessionNotFoundError("Unknown session: " + sessionID)
	}
	return metadata, nil
}

// OpenSession counts the open and waits at the next open gate. It then fails for an unseeded Session or with the next open error, or records and returns a new TestHarness.
func (h *TestServerHost) OpenSession(_ context.Context, metadata routing.SessionMetadata) (routing.RoutedSessionHandle, error) {
	h.mu.Lock()
	h.openSessionCount++
	gate := h.nextOpenSessionGate
	h.nextOpenSessionGate = nil
	h.mu.Unlock()
	if gate != nil {
		gate.pass()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.sessions[metadata.SessionID()]; !ok {
		return nil, routing.NewSessionNotFoundError("Unknown session: " + metadata.SessionID())
	}
	if failure := h.nextOpenSessionError; failure != nil {
		h.nextOpenSessionError = nil
		return nil, failure
	}
	harness := NewTestHarness(metadata)
	if h.nextHarnessCloseError != nil {
		harness.failClose = h.nextHarnessCloseError
		h.nextHarnessCloseError = nil
	}
	h.harnesses[metadata.SessionID()] = append(h.harnesses[metadata.SessionID()], harness)
	return harness, nil
}

// Seed records a Session with ID id, or "session-1" when id is nil, and returns its metadata.
func (h *TestServerHost) Seed(id *string) routing.SessionMetadata {
	sessionID := "session-1"
	if id != nil {
		sessionID = *id
	}
	metadata := routing.BasicSessionMetadata{ID: sessionID}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessions[sessionID] = metadata
	return metadata
}

// GateNextOpenSession pauses the next OpenSession at the returned gate.
func (h *TestServerHost) GateNextOpenSession() *OpenGate {
	gate := newOpenGate()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextOpenSessionGate = gate
	return gate
}

// LatestHarness returns the most recently opened harness for a Session.
func (h *TestServerHost) LatestHarness(id string) (*TestHarness, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	harnesses := h.harnesses[id]
	if len(harnesses) == 0 {
		return nil, errors.New("No harness for " + id)
	}
	return harnesses[len(harnesses)-1], nil
}
