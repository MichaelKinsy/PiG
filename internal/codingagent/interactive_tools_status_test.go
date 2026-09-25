package codingagent

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

type managedToolTransport func(*http.Request) (*http.Response, error)

func (f managedToolTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Pi init awaits both ensureTool calls, but each onStatus renders before its download finishes.
func TestManagedToolsRenderProgressBeforeDownloadsFinish(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("PIG_OFFLINE", "")
	t.Setenv("PI_OFFLINE", "")
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		var requests atomic.Int32
		http.DefaultTransport = managedToolTransport(func(r *http.Request) (*http.Response, error) {
			requests.Add(1)
			<-release
			return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
		})
		m := &InteractiveMode{chatContainer: tui.NewContainer()}
		done := make(chan struct{})
		go func() {
			defer close(done)
			m.ensureManagedTools(t.Context(), tools.NewToolsManager(dir))
		}()
		synctest.Wait()
		// Each release lookup must be in flight before either is allowed to finish.
		if got := requests.Load(); got != 2 {
			t.Errorf("concurrent release lookups = %d, want fd and rg", got)
		}
		lines := strings.Join(m.chatContainer.Render(160), "\n")
		for _, name := range []string{"fd", "ripgrep"} {
			if !strings.Contains(lines, name+" not found. Downloading...") {
				t.Errorf("missing live %s progress: %q", name, lines)
			}
		}
		if strings.Index(lines, "fd not found.") > strings.Index(lines, "ripgrep not found.") {
			t.Errorf("initial reports are not in Promise.all argument order: %q", lines)
		}
		select {
		case <-done:
			t.Error("startup finished before the downloads")
		default:
		}
		close(release)
		<-done
		lines = strings.Join(m.chatContainer.Render(160), "\n")
		for _, name := range []string{"fd", "ripgrep"} {
			if !strings.Contains(lines, "Warning: Failed to download "+name+": ") {
				t.Errorf("missing terminal %s status: %q", name, lines)
			}
		}
	})
}

func TestManagedToolsCancellationJoinsDownloads(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("PIG_OFFLINE", "")
	t.Setenv("PI_OFFLINE", "")
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		release := make(chan struct{})
		var active atomic.Int32
		http.DefaultTransport = managedToolTransport(func(r *http.Request) (*http.Response, error) {
			active.Add(1)
			defer active.Add(-1)
			select {
			case <-r.Context().Done():
				return nil, r.Context().Err()
			case <-release:
				return nil, io.EOF
			}
		})
		m := &InteractiveMode{chatContainer: tui.NewContainer()}
		done := make(chan struct{})
		go func() {
			defer close(done)
			m.ensureManagedTools(ctx, tools.NewToolsManager(dir))
		}()
		synctest.Wait()
		cancel()
		synctest.Wait()
		select {
		case <-done:
			if got := active.Load(); got != 0 {
				t.Errorf("startup left %d requests active", got)
			}
		default:
			t.Error("startup ignored cancellation")
		}
		close(release)
		<-done
	})
}
