package coding

import (
	"context"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// pendingWarmProvider delays a refresh's completion after cancellation, including creation of a session-scoped resource during provider teardown.
type pendingWarmProvider struct {
	warmingProvider
	started  chan context.Context
	release  chan struct{}
	resource atomic.Bool
}

func (p *pendingWarmProvider) Stream(ctx context.Context, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	if options.MaxTokens == 1 {
		p.started <- ctx
		<-ctx.Done()
		<-p.release
		p.resource.Store(true)
	}
	return p.warmingProvider.Stream(ctx, transcript, options)
}

func newPendingWarmSession(t *testing.T) (*Session, *pendingWarmProvider, context.Context) {
	t.Helper()
	t.Setenv("PI_CACHE_RETENTION", "short")
	provider := &pendingWarmProvider{started: make(chan context.Context, 1), release: make(chan struct{})}
	sess, err := NewSession(warmingServices(t, "idle"), SessionOptions{NoSession: true, Model: warmingModel(provider)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	sendAndSettle(t, sess)
	status := sess.CacheWarmingStatus()
	if status == nil || status.State != "scheduled" {
		t.Fatalf("status = %+v, want scheduled refresh", status)
	}
	time.Sleep(time.Until(time.UnixMilli(status.NextWarmAt)))
	return sess, provider, <-provider.started
}

// Upstream AgentSession.dispose cancels the warmer before cleanupSessionResources. Go also joins the refresh so a late provider completion cannot recreate a resource after cleanup.
func TestSessionCloseCancelsAndDrainsWarmerBeforeProviderCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sess, provider, warmCtx := newPendingWarmSession(t)
		var cleanupCalled atomic.Bool
		unregister := ai.RegisterSessionResourceCleanup(func(id string) {
			if id != sess.ID() {
				return
			}
			cleanupCalled.Store(true)
			if warmCtx.Err() == nil {
				t.Error("provider resource cleanup ran while cache-warming request was still live")
			}
			if !provider.resource.Swap(false) {
				t.Error("provider resource cleanup ran before the pending refresh released its last resource")
			}
		})
		defer unregister()
		closed := make(chan struct{})
		go func() {
			_ = sess.Close()
			close(closed)
		}()
		synctest.Wait()
		if warmCtx.Err() == nil {
			t.Error("Close did not cancel the pending refresh")
		}
		if cleanupCalled.Load() {
			t.Error("cleanup ran before the refresh drained")
		}
		select {
		case <-closed:
			t.Error("Close returned before the refresh drained")
		default:
		}
		close(provider.release)
		<-closed
		if !cleanupCalled.Load() {
			t.Error("Close did not run provider resource cleanup")
		}
		if provider.resource.Load() {
			t.Error("late refresh completion left a provider resource alive after Close")
		}
		for _, entry := range sess.Inner().GetBranch() {
			if entry.Base.Type == "usage" {
				t.Error("cancelled refresh persisted late usage")
			}
		}
	})
}

// Upstream drops disposed AgentSessions on replacement. A stopped timer must not keep their session managers and transcript caches alive.
func TestIdleReplacementReleasesOldCacheWarmingSessions(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		scheduled  bool
	}{
		{name: "off", mode: "off"},
		{name: "idle", mode: "idle"},
		{name: "streaming", mode: "streaming"},
		{name: "scheduled", mode: "idle", scheduled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				sess, _ := newWarmingSession(t, tc.mode, SessionOptions{NoSession: true})
				if tc.scheduled {
					sendAndSettle(t, sess)
				}
				var retired []weak.Pointer[icodingagent.Session]
				for range 32 {
					retired = append(retired, weak.Make(sess.Inner()))
					sess.ReplaceInner(icodingagent.NewSession("replacement", sess.CWD()))
				}
				synctest.Wait()
				runtime.GC()
				for i, old := range retired {
					if old.Value() != nil {
						t.Errorf("fully idle old Session %d retained by closed cache warmer after replacement", i)
					}
				}
				runtime.KeepAlive(sess)
			})
		})
	}
}

// Replacement cancels immediately without waiting for a slow provider. Only actually pending refreshes retain old Sessions, and Session.Close joins those refreshes too.
func TestReplacementDrainsAndReleasesInFlightCacheWarmer(t *testing.T) {
	for _, closeSession := range []bool{false, true} {
		name := "while_open"
		if closeSession {
			name = "during_close"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				sess, provider, warmCtx := newPendingWarmSession(t)
				old := weak.Make(sess.Inner())
				sess.ReplaceInner(icodingagent.NewSession("replacement", sess.CWD()))
				if warmCtx.Err() == nil {
					t.Error("replacement did not cancel the pending refresh")
				}
				if status := sess.CacheWarmingStatus(); status.Reason != "waiting for first request" {
					t.Errorf("replacement status = %+v", status)
				}
				synctest.Wait()
				runtime.GC()
				if old.Value() == nil {
					t.Error("pending refresh lost its old Session before draining")
				}
				var closed chan struct{}
				if closeSession {
					closed = make(chan struct{})
					go func() {
						_ = sess.Close()
						close(closed)
					}()
					synctest.Wait()
					select {
					case <-closed:
						t.Error("Close returned before the retired refresh drained")
					default:
					}
				}
				close(provider.release)
				if closed != nil {
					<-closed
				}
				synctest.Wait()
				runtime.GC()
				if old.Value() != nil {
					t.Error("drained refresh still retains the replaced Session")
				}
				for _, entry := range sess.Inner().GetBranch() {
					if entry.Base.Type == "usage" {
						t.Error("retired refresh appended usage to the replacement Session")
					}
				}
				runtime.KeepAlive(sess)
			})
		})
	}
}

// Exercise repeated /new and resume-shaped replacements, including transcript restoration and disposal of the previous warmer.
func BenchmarkSessionReplaceInnerCacheWarming(b *testing.B) {
	for _, entries := range []int{0, 128} {
		name := "empty"
		if entries > 0 {
			name = "history"
		}
		b.Run(name, func(b *testing.B) {
			b.Setenv("PIG_HOME", b.TempDir())
			services, err := NewServices(ServicesOptions{CWD: b.TempDir()})
			if err != nil {
				b.Fatal(err)
			}
			if err := services.SettingsManager().SetCacheWarmingMode("off"); err != nil {
				b.Fatal(err)
			}
			sess, err := NewSession(services, SessionOptions{Model: warmingModel(&warmingProvider{}), NoSession: true})
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() {
				_ = sess.Close()
				for range sess.Events() {
				}
			})
			message := agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: strings.Repeat("history ", 128)}}}}
			b.ReportAllocs()
			for b.Loop() {
				inner := icodingagent.NewSession("replacement", sess.CWD())
				for range entries {
					if _, err := inner.AppendMessage(message); err != nil {
						b.Fatal(err)
					}
				}
				sess.ReplaceInner(inner)
			}
		})
	}
}
