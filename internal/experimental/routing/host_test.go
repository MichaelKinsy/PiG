package routing_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
)

// openGate is upstream OpenGate: entered closes when the gated operation starts, and the operation resumes when release closes.
type openGate struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

// open resumes the gated operation. It is safe to call twice, so a failed barrier still releases the gate in cleanup.
func (gate *openGate) open() { gate.once.Do(func() { close(gate.release) }) }

func newOpenGate() *openGate {
	return &openGate{entered: make(chan struct{}), release: make(chan struct{})}
}

func (gate *openGate) wait() {
	close(gate.entered)
	<-gate.release
}

// testHarness is upstream TestHarness (packages/server/src/testing/host.ts:27-117).
type testHarness struct {
	session session.Session

	mu                     sync.Mutex
	attachedClients        int
	attachmentReleaseCount int
	closeCount             int
	serviceCalls           []chord.ServiceCall
	failAttachmentRelease  error
	failClose              error
	nextServiceError       error
	nextServiceResult      json.RawMessage
	nextCloseGate          *openGate
	nextServiceGate        *openGate
	terminated             chan struct{}
	terminalError          error
	terminationOnce        sync.Once
	closedSignal           chan struct{}
	closedOnce             sync.Once
}

func newTestHarness(opened session.Session) *testHarness {
	return &testHarness{session: opened, nextServiceResult: json.RawMessage(`{"ok":true}`), terminated: make(chan struct{}), closedSignal: make(chan struct{})}
}

func (harness *testHarness) Terminated() <-chan struct{} { return harness.terminated }
func (harness *testHarness) TerminalError() error {
	harness.mu.Lock()
	defer harness.mu.Unlock()
	return harness.terminalError
}

func (harness *testHarness) terminate(failure error) {
	harness.terminationOnce.Do(func() {
		harness.mu.Lock()
		harness.terminalError = failure
		harness.mu.Unlock()
		close(harness.terminated)
	})
}

func (harness *testHarness) AttachClient(context.Context) (routing.RoutedSessionAttachment, error) {
	harness.mu.Lock()
	harness.attachedClients++
	harness.mu.Unlock()
	return &testHarnessAttachment{harness: harness}, nil
}

type testHarnessAttachment struct {
	harness  *testHarness
	mu       sync.Mutex
	released bool
}

func (attachment *testHarnessAttachment) InvokeService(_ context.Context, call chord.ServiceCall, _ chord.ServiceUpdatePublisher) (json.RawMessage, error) {
	return attachment.harness.invokeService(call)
}

func (attachment *testHarnessAttachment) Release(context.Context) error {
	attachment.mu.Lock()
	defer attachment.mu.Unlock()
	if attachment.released {
		return nil
	}
	harness := attachment.harness
	harness.mu.Lock()
	defer harness.mu.Unlock()
	harness.attachmentReleaseCount++
	if harness.failAttachmentRelease != nil {
		return harness.failAttachmentRelease
	}
	attachment.released = true
	harness.attachedClients--
	return nil
}

func (harness *testHarness) invokeService(call chord.ServiceCall) (json.RawMessage, error) {
	harness.mu.Lock()
	harness.serviceCalls = append(harness.serviceCalls, call)
	if failure := harness.nextServiceError; failure != nil {
		harness.nextServiceError = nil
		harness.mu.Unlock()
		return nil, failure
	}
	gate := harness.nextServiceGate
	harness.nextServiceGate = nil
	harness.mu.Unlock()
	if gate != nil {
		gate.wait()
	}
	harness.mu.Lock()
	defer harness.mu.Unlock()
	result := harness.nextServiceResult
	harness.nextServiceResult = json.RawMessage(`{"ok":true}`)
	return result, nil
}

func (harness *testHarness) Close(ctx context.Context) error {
	harness.mu.Lock()
	harness.closeCount++
	gate := harness.nextCloseGate
	harness.nextCloseGate = nil
	harness.mu.Unlock()
	if gate != nil {
		gate.wait()
	}
	harness.mu.Lock()
	failure := harness.failClose
	harness.failClose = nil
	harness.mu.Unlock()
	if failure != nil {
		return failure
	}
	if err := harness.session.Close(ctx); err != nil {
		return err
	}
	harness.closedOnce.Do(func() { close(harness.closedSignal) })
	harness.terminate(nil)
	return nil
}

func (harness *testHarness) terminateWithError(ctx context.Context, failure error) error {
	if err := harness.session.Close(ctx); err != nil {
		return err
	}
	harness.terminate(failure)
	return nil
}

// waitAttachedClients waits until the attachment count equals want; upstream expect.poll.
func (harness *testHarness) waitAttachedClients(t *testing.T, want int) {
	t.Helper()
	pollUntil(t, fmt.Sprintf("attachedClients == %d", want), func() bool { return harness.attached() == want })
}

func (harness *testHarness) attached() int {
	harness.mu.Lock()
	defer harness.mu.Unlock()
	return harness.attachedClients
}

func (harness *testHarness) closes() int {
	harness.mu.Lock()
	defer harness.mu.Unlock()
	return harness.closeCount
}

func (harness *testHarness) releases() int {
	harness.mu.Lock()
	defer harness.mu.Unlock()
	return harness.attachmentReleaseCount
}

func (harness *testHarness) calls() []chord.ServiceCall {
	harness.mu.Lock()
	defer harness.mu.Unlock()
	return append([]chord.ServiceCall(nil), harness.serviceCalls...)
}

// testServerHost is upstream TestServerHost over a MemorySessionRepo.
type testServerHost struct {
	repo *session.MemorySessionRepo

	mu                   sync.Mutex
	harnesses            map[string][]*testHarness
	openSessionCount     int
	nextOpenSessionError error
	nextHarnessCloseErr  error
	nextOpenSessionGate  *openGate
}

func newTestServerHostWithSessions() *testServerHost {
	return &testServerHost{repo: session.NewMemorySessionRepo(&session.MemorySessionRepoOptions{Now: func() int64 { return 1 }}), harnesses: map[string][]*testHarness{}}
}

func (host *testServerHost) serverHost() routing.ServerHost {
	return routing.ServerHost{ServerServices: testServerServices{}, ResolveSession: host.resolveSession, OpenSession: host.openSession}
}

func (host *testServerHost) resolveSession(ctx context.Context, sessionID string) (session.SessionMetadata, error) {
	listed, err := host.repo.List(ctx)
	if err != nil {
		return session.SessionMetadata{}, err
	}
	var matches []session.SessionMetadata
	for _, metadata := range listed {
		if metadata.ID == sessionID {
			matches = append(matches, metadata)
		}
	}
	switch len(matches) {
	case 0:
		return session.SessionMetadata{}, routing.NewSessionNotFoundError("Unknown session: " + sessionID)
	case 1:
		return matches[0], nil
	}
	return session.SessionMetadata{}, routing.NewSessionAmbiguousError()
}

func (host *testServerHost) openSession(ctx context.Context, metadata session.SessionMetadata) (routing.RoutedSessionHandle, error) {
	host.mu.Lock()
	host.openSessionCount++
	gate := host.nextOpenSessionGate
	host.nextOpenSessionGate = nil
	host.mu.Unlock()
	if gate != nil {
		gate.wait()
	}
	opened, err := host.repo.Open(ctx, metadata)
	if err != nil {
		return nil, err
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	if failure := host.nextOpenSessionError; failure != nil {
		host.nextOpenSessionError = nil
		_ = opened.Close(ctx)
		return nil, failure
	}
	harness := newTestHarness(opened)
	if host.nextHarnessCloseErr != nil {
		harness.failClose = host.nextHarnessCloseErr
		host.nextHarnessCloseErr = nil
	}
	host.harnesses[metadata.ID] = append(host.harnesses[metadata.ID], harness)
	return harness, nil
}

func (host *testServerHost) seed(t *testing.T, id string) session.SessionMetadata {
	t.Helper()
	opened, err := host.repo.Create(context.Background(), session.SessionCreateOptions{ID: id})
	if err != nil {
		t.Fatal(err)
	}
	metadata := opened.Metadata()
	if err := opened.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	return metadata
}

func (host *testServerHost) latestHarness(t *testing.T, id string) *testHarness {
	t.Helper()
	host.mu.Lock()
	defer host.mu.Unlock()
	harnesses := host.harnesses[id]
	if len(harnesses) == 0 {
		t.Fatalf("no harness for %s", id)
	}
	return harnesses[len(harnesses)-1]
}

func (host *testServerHost) harnessCount(id string) int {
	host.mu.Lock()
	defer host.mu.Unlock()
	return len(host.harnesses[id])
}

func (host *testServerHost) totalHarnesses() int {
	host.mu.Lock()
	defer host.mu.Unlock()
	return len(host.harnesses)
}

func (host *testServerHost) opened() int {
	host.mu.Lock()
	defer host.mu.Unlock()
	return host.openSessionCount
}

func (host *testServerHost) gateNextOpenSession(t *testing.T) *openGate {
	t.Helper()
	gate := newOpenGate()
	// A barrier that fails with t.Fatal would otherwise leave the gated open parked, and the server's cleanup Close would wait on it until the test binary times out.
	t.Cleanup(gate.open)
	host.mu.Lock()
	host.nextOpenSessionGate = gate
	host.mu.Unlock()
	return gate
}
