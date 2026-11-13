package coding

// pi: packages/coding-agent/src/core/agent-session.ts

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// slowTokenEndpoint is the transport of a summaryAuthWorld whose OAuth token endpoint, once armed, does not answer until release.
type slowTokenEndpoint struct {
	next    http.RoundTripper
	armed   atomic.Bool
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (s *slowTokenEndpoint) RoundTrip(req *http.Request) (*http.Response, error) {
	if s.armed.Load() && req.URL.Host == "platform.claude.com" {
		s.once.Do(func() { close(s.started) })
		<-s.release
	}
	return s.next.RoundTrip(req)
}

// slowAuthSession is a Session over a registry-built provider (Anthropic's subscription login) whose OAuth credential has expired and whose refresh stays in flight until the test ends. It is not built by CreateAgentSession, so its stream function is not the Model Runtime's and a summary resolves its request auth before it sends anything (agent-session.ts:623-646). refreshing is closed when the refresh has started.
func slowAuthSession(t *testing.T) (session *Session, refreshing <-chan struct{}, completer *fakeCompleter) {
	t.Helper()
	var shape summaryAuthShape
	for _, candidate := range summaryAuthShapes() {
		if candidate.name == "plain-oauth-anthropic" {
			shape = candidate
		}
	}
	world := newSummaryAuthWorld(t)
	slow := &slowTokenEndpoint{next: world, started: make(chan struct{}), release: make(chan struct{})}
	http.DefaultTransport = slow
	t.Cleanup(func() { http.DefaultTransport = world })
	agentDir := t.TempDir()
	writeAgentFile(t, agentDir, "settings.json", summaryAuthSettings)
	resolve := shape.setup(t, world, agentDir)
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	session, err = NewSession(services, SessionOptions{Model: resolve(services)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	// The refresh is released, and its credential saved, before the Session and the services close and the agent directory is removed.
	t.Cleanup(func() {
		close(slow.release)
		_ = services.ModelRuntime().SettleCredentialWrites(context.Background())
	})
	services.SettingsManager().ApplyOverrides(icodingagent.Settings{Compaction: &icodingagent.CompactionSettingsJSON{KeepRecentTokens: new(1.)}})
	for _, text := range []string{"q0", "q1", "q2"} {
		for _, message := range []agent.AgentMessage{
			{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: text}}}},
			{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "answer to " + text}}}},
		} {
			if _, err := session.inner.Load().AppendMessage(message); err != nil {
				t.Fatal(err)
			}
		}
	}
	session.agent.SetMessages(session.inner.Load().BuildContext(nil))
	completer = &fakeCompleter{summary: "must not run"}
	session.completer = completer
	world.advance(services, shape.credentialProvider, 31*time.Minute)
	slow.armed.Store(true)
	return session, slow.started, completer
}

// agent-session.ts:605-646 (_getSummarizationRequestAuth) hands the signal of the summary to modelRuntime.getAuth(model, { signal })
// (:576, :631), and getAuth rejects with the signal's reason as soon as it aborts, while the credential is still being resolved
// (resolve.ts:33-44). Each summary passes its own signal: compact() the compaction controller's (:2886), navigateTree the branch
// summary controller's (:4224), summarizeForBugReport its caller's (:4495). The operation ends with that reason and sends no request.
func TestSummaryAbortEndsARequestAuthStillBeingResolved(t *testing.T) {
	for _, operation := range []struct {
		name  string
		run   func(ctx context.Context, session *Session) error
		abort func(session *Session, cancel context.CancelFunc)
	}{
		{"compaction", func(ctx context.Context, session *Session) error {
			_, err := session.Compact(ctx, "")
			return err
		}, func(session *Session, _ context.CancelFunc) { session.AbortCompaction() }},
		{"branch-summary", func(ctx context.Context, session *Session) error {
			var target string
			for _, entry := range session.inner.Load().GetEntries() {
				if entry.Base().Type == "message" {
					target = entry.Base().ID
					break
				}
			}
			_, err := session.NavigateTree(ctx, target, NavigateTreeOptions{Summarize: true})
			return err
		}, func(session *Session, _ context.CancelFunc) { session.AbortBranchSummary() }},
		{"bug-report", func(ctx context.Context, session *Session) error {
			_, err := session.SummarizeForBugReport(ctx, "it broke")
			return err
		}, func(_ *Session, cancel context.CancelFunc) { cancel() }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			session, refreshing, completer := slowAuthSession(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- operation.run(ctx, session) }()
			select {
			case <-refreshing:
			case err := <-done:
				t.Fatalf("the summary ended with %v before it resolved its request auth", err)
			case <-time.After(5 * time.Second):
				t.Fatal("the summary did not resolve its request auth")
			}
			operation.abort(session, cancel)
			select {
			case err := <-done:
				if !errors.Is(err, errOperationAborted) {
					t.Fatalf("error = %v, want %v", err, errOperationAborted)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the summary did not end when it was aborted while its request auth was being resolved: the auth resolution does not carry the summary's signal")
			}
			if completer.called.Load() {
				t.Fatal("the summary request was sent after the abort")
			}
		})
	}
}
