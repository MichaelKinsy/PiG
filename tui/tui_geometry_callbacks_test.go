package tui

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// geometryRenderer is the part of a renderer the geometry callback tests drive.
type geometryRenderer interface {
	SetOnWidthChange(fn func(width int))
	SetOnHeightChange(fn func(height int))
	SetFixedSize(cols, rows int)
	Start()
	Render()
	StopWithOptions(options StopOptions)
}

// geometryRenderers are the renderers off the alternate screen, 20 columns by 4 rows, with scheduled renders dropped. The
// alternate screen's own tests hold it to the same rule (TestAltScreenGeometryCallbacksCoalesceInRenderOrder).
var geometryRenderers = []struct {
	name string
	open func() geometryRenderer
}{
	{"main screen", func() geometryRenderer {
		tui := NewWithOutput(&bytes.Buffer{}, 20, 4)
		tui.SetRenderDispatcher(func(func()) {})
		return tui
	}},
	{"surface", func() geometryRenderer {
		tui := NewTuiSurfaceWithSize(newRecordingSession(), 20, 4, nil)
		tui.SetRenderDispatcher(func(func()) {})
		return tui
	}},
}

// A geometry callback tells its listener the size a frame was rendered at; the subprocess extension host keeps the width as
// the one extensions render for. Pi's components are given the width inside the render itself, which reads the terminal
// size once and returns at once when stopped (tui-main-screen.ts:249-252), and resizes that arrive before the next render
// share it (tui.ts:1033-1042). So a listener is never given a size while it is still taking an earlier one, never an older
// size after a newer one, and the sizes that pass while it is busy collapse into the latest.
func TestGeometryCallbacksCoalesceInRenderOrder(t *testing.T) {
	for _, renderer := range geometryRenderers {
		for _, tc := range []struct {
			name    string
			first   [2]int
			resizes [][2]int
			want    []string
		}{
			{"width", [2]int{12, 4}, [][2]int{{20, 9}, {8, 6}, {8, 6}}, []string{"width:12", "width:8", "height:6"}},
			{"height", [2]int{20, 6}, [][2]int{{12, 9}, {8, 4}, {8, 4}}, []string{"height:6", "width:8", "height:4"}},
		} {
			t.Run(renderer.name+"/"+tc.name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					tui := renderer.open()
					started, release := make(chan struct{}), make(chan struct{})
					events := make(chan string, 16)
					notify := func(kind string, value int) {
						event := fmt.Sprintf("%s:%d", kind, value)
						if event == tc.want[0] {
							close(started)
							<-release
						}
						events <- event
					}
					tui.SetOnWidthChange(func(width int) { notify("width", width) })
					tui.SetOnHeightChange(func(height int) { notify("height", height) })
					tui.Start()
					t.Cleanup(func() { tui.StopWithOptions(StopOptions{PreserveScreen: true}) })
					tui.Render()
					tui.SetFixedSize(tc.first[0], tc.first[1])
					tui.Render()
					<-started
					for _, size := range tc.resizes {
						tui.SetFixedSize(size[0], size[1])
						tui.Render()
					}
					synctest.Wait()
					var overtook []string
					for len(events) > 0 {
						overtook = append(overtook, <-events)
					}
					if len(overtook) != 0 {
						t.Errorf("callbacks %v overtook the blocked %s callback", overtook, tc.want[0])
					}
					close(release)
					synctest.Wait()
					got := overtook
					for len(events) > 0 {
						got = append(got, <-events)
					}
					if !slices.Equal(got, tc.want) {
						t.Fatalf("geometry callbacks = %v; want %v", got, tc.want)
					}
				})
			})
		}
	}
}

// A stop discards the sizes not yet given to the listener, and a restart begins with the sizes rendered after it.
func TestGeometryCallbacksStopDiscardsPendingSizes(t *testing.T) {
	for _, renderer := range geometryRenderers {
		t.Run(renderer.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				tui := renderer.open()
				started, release := make(chan struct{}), make(chan struct{})
				events := make(chan string, 8)
				tui.SetOnWidthChange(func(width int) {
					if width == 12 {
						close(started)
						<-release
					}
					events <- fmt.Sprintf("width:%d", width)
				})
				tui.SetOnHeightChange(func(height int) { events <- fmt.Sprintf("height:%d", height) })
				tui.Start()
				t.Cleanup(func() { tui.StopWithOptions(StopOptions{PreserveScreen: true}) })
				tui.Render()
				tui.SetFixedSize(12, 4)
				tui.Render()
				<-started
				tui.SetFixedSize(8, 6)
				tui.Render()
				tui.StopWithOptions(StopOptions{PreserveScreen: true})
				tui.Start()
				close(release)
				synctest.Wait()
				tui.SetFixedSize(30, 10)
				tui.Render()
				synctest.Wait()
				var got []string
				for len(events) > 0 {
					got = append(got, <-events)
				}
				if want := []string{"width:12", "width:30", "height:10"}; !slices.Equal(got, want) {
					t.Fatalf("geometry callbacks across restart = %v; want %v", got, want)
				}
			})
		})
	}
}

// gatedSession holds the first frame applied once armed until gate is closed.
type gatedSession struct {
	*recordingSession
	armed         atomic.Bool
	entered, gate chan struct{}
}

func (s *gatedSession) Apply(frame frontend.Frame) error {
	if s.armed.CompareAndSwap(true, false) {
		close(s.entered)
		<-s.gate
	}
	return s.recordingSession.Apply(frame)
}

// The surface gives a frame to its session once the render has let go of the renderer, where a Stop on another goroutine can
// come first. The sizes of that frame are not given to the listener after the Stop returned.
func TestSurfaceGeometryCallbacksAreNotCalledAfterStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		session := &gatedSession{recordingSession: newRecordingSession(), entered: make(chan struct{}), gate: make(chan struct{})}
		tui := NewTuiSurfaceWithSize(session, 20, 4, nil)
		tui.SetRenderDispatcher(func(func()) {})
		var stopped atomic.Bool
		events := make(chan string, 8)
		notify := func(kind string, value int) {
			event := fmt.Sprintf("%s:%d", kind, value)
			if stopped.Load() {
				event += " after Stop"
			}
			events <- event
		}
		tui.SetOnWidthChange(func(width int) { notify("width", width) })
		tui.SetOnHeightChange(func(height int) { notify("height", height) })
		file := "/sessions/a.jsonl"
		tui.SetHooks(SurfaceHooks{SessionFile: func() string { return file }})
		tui.Start()
		t.Cleanup(tui.Stop)
		// The new session file makes the frame of the resize one the session is given.
		tui.SetFixedSize(12, 6)
		file = "/sessions/b.jsonl"
		session.armed.Store(true)
		rendered := make(chan struct{})
		go func() {
			tui.Render()
			close(rendered)
		}()
		<-session.entered
		synctest.Wait()
		tui.Stop()
		stopped.Store(true)
		close(session.gate)
		<-rendered
		synctest.Wait()
		var got []string
		for len(events) > 0 {
			got = append(got, <-events)
		}
		if slices.ContainsFunc(got, func(event string) bool { return strings.HasSuffix(event, " after Stop") }) {
			t.Fatalf("geometry callbacks = %v; want none after Stop returned", got)
		}
	})
}
