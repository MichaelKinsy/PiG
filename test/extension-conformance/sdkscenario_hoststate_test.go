package extensionconformance

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// The host state every scenario rig serves. It is deliberately not the value an
// SDK would answer with no host data: a getter that returns a constant instead
// of asking the host fails the scenario that reads it.
const (
	rigSystemPrompt  = "scenario system prompt"
	rigThinkingLevel = "high"
)

// hostCallLog records the fire-and-forget host actions an extension triggered.
type hostCallLog struct {
	mu      sync.Mutex
	lines   []string
	taken   int
	changed chan struct{}
}

// take returns the calls recorded since the previous take.
func (l *hostCallLog) take() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	fresh := append([]string(nil), l.lines[l.taken:]...)
	l.taken = len(l.lines)
	return fresh
}

func (l *hostCallLog) add(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, line)
	if l.changed != nil {
		close(l.changed)
		l.changed = nil
	}
}

// wait returns once the log holds n calls, or when ctx ends.
func (l *hostCallLog) wait(ctx context.Context, n int) {
	for {
		l.mu.Lock()
		if len(l.lines) >= n {
			l.mu.Unlock()
			return
		}
		if l.changed == nil {
			l.changed = make(chan struct{})
		}
		changed := l.changed
		l.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return
		}
	}
}

func (l *hostCallLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

// installHostState binds the host actions an extension's context reads and
// calls: the agent state getters, the session manager reads over a fixed
// session, and the abort, shutdown and compact actions, which record themselves.
func (r *scenarioRig) installHostState() {
	b := r.bridge
	r.footer.set("main", map[string]string{"a": "one"}, 2)
	b.SetHostAction("getGitBranch", r.footer.branch)
	b.SetHostAction("getExtensionStatuses", r.footer.statuses)
	b.SetHostAction("getAvailableProviderCount", r.footer.count)
	b.SetHostAction("getSignal", r.signal.current)
	b.SetHostAction("isIdle", func() bool { return false })
	b.SetHostAction("hasPendingMessages", func() bool { return true })
	b.SetHostAction("getSystemPrompt", func() string { return rigSystemPrompt })
	b.SetHostAction("getThinkingLevel", func() string { return rigThinkingLevel })
	b.SetHostAction("getAllTools", func() []subprocess.ToolInfo {
		return []subprocess.ToolInfo{
			{Name: "read", Description: "Read files", Parameters: json.RawMessage(`{"type":"object"}`), Exposure: extension.ToolExposure("direct")},
			{Name: "grep", Description: "Search files", Parameters: json.RawMessage(`{"type":"object"}`), Exposure: extension.ToolExposure("direct")},
		}
	})
	b.SetHostAction("getModelRegistryState", func() map[string]any {
		return map[string]any{
			"models": []any{},
			"providers": map[string]any{
				"configured": map[string]any{"name": "Configured", "configured": true, "usingOAuth": true, "authStatus": map[string]any{"configured": true, "source": "stored"}},
				"keyonly":    map[string]any{"name": "Key only", "configured": true, "usingOAuth": false, "authStatus": map[string]any{"configured": true, "source": "env"}},
				"plain":      map[string]any{"name": "Plain", "configured": false, "usingOAuth": false, "authStatus": map[string]any{"configured": false}},
			},
		}
	})
	b.SetHostAction("abort", func() { r.calls.add("abort") })
	// ctx.reload() reloads the host as the command-context action of a mode does (agent-session.ts reload).
	b.SetHostAction("reload", func(ctx context.Context) error {
		_, err := r.host.Reload(ctx)
		return err
	})
	b.SetHostAction("shutdown", func() { r.calls.add("shutdown") })
	b.SetHostAction("compact", func(_ context.Context, opts *extension.CompactOptions) {
		instructions := ""
		if opts != nil {
			instructions = opts.CustomInstructions
		}
		r.calls.add("compact instructions=" + instructions)
	})
	view := registryProbeView()
	path := filepath.Join(r.dir, "session.jsonl")
	header, err := json.Marshal(view.Session.GetHeader())
	if err != nil {
		r.t.Fatal(err)
	}
	data := slices.Concat(header, []byte{'\n'})
	for _, entry := range view.Session.GetEntries() {
		data = append(append(data, entry.Raw()...), '\n')
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		r.t.Fatal(err)
	}
	view.Session.SetPath(path)
	b.SetHostAction("sessionRead", func(method string, args json.RawMessage) (any, error) {
		return codingagent.ExtensionSessionRead(view, method, args)
	})
	b.SetHostAction("getSessionID", func() string { return view.Session.ID() })
	b.SetHostAction("getSessionName", func() string { return "" })
	b.SetHostAction("getSessionFile", func() string { return path })
	b.SetHostAction("getLeafID", func() string { return "two" })
}

// runSignalState is the cancellation of the run the host reports: absent
// between runs, live during one, cancelled once the run was aborted.
type runSignalState struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
}

func (s *runSignalState) current() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ctx
}

func (s *runSignalState) begin() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctx, s.cancel = context.WithCancel(context.Background())
}

func (s *runSignalState) abort() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *runSignalState) end() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	s.ctx, s.cancel = nil, nil
}

// footerData is what the host's footer reports: the Git branch, the extension statuses and the available provider count.
type footerData struct {
	mu        sync.Mutex
	gitBranch string
	status    map[string]string
	providers int
}

func (f *footerData) set(branch string, statuses map[string]string, providers int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gitBranch, f.status, f.providers = branch, maps.Clone(statuses), providers
}

func (f *footerData) branch() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gitBranch
}

func (f *footerData) statuses() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.status)
}

func (f *footerData) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.providers
}

// rigUI is a UI context that is not the no-op one, so the host reports a UI to the extensions; every surface is the no-op's.
type rigUI struct{ extension.UIContext }
