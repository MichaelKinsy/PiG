package codingagent

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi 0.87.1 agent-session.ts:3294-3313 awaits resource-loader.ts:405 before rebuilding and session_start.
// package-manager.ts:1304,1960-1970,2671-2683 awaits the temporary Git child without blocking the UI event loop.
// interactive-mode.ts:6195-6206,6223 keeps the reload box focused until that work completes.
func TestReloadResourceResolutionPumpsOwner(t *testing.T) {
	for _, snapshotProvider := range []bool{true, false} {
		name := "snapshot"
		if !snapshotProvider {
			name = "metadata"
		}
		for _, cancelOwner := range []bool{false, true} {
			suffix := "/complete"
			if cancelOwner {
				suffix = "/cancel"
			}
			t.Run(name+suffix, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					ctx, cancel := context.WithCancelCause(t.Context())
					defer cancel(nil)
					entered, release := make(chan struct{}), make(chan struct{})
					old := map[string]ResourceSourceInfo{"old": {Path: "old"}}
					fresh := map[string]ResourceSourceInfo{"fresh": {Path: "fresh"}}
					var order []string
					resolve := func() map[string]ResourceSourceInfo {
						order = append(order, "resolve")
						close(entered)
						<-release // A pending child exit, not a detached or completed refresh.
						return fresh
					}
					opts := InteractiveModeOptions{
						Settings: Settings{Theme: "dark"}, NoThemes: true, NoSkills: true, NoPromptTemplates: true,
						StageExtensionSDKs: func() error { order = append(order, "stage"); return nil },
					}
					if snapshotProvider {
						opts.ReloadResourceProvider = func() ReloadResourceSnapshot {
							return ReloadResourceSnapshot{ResourceSourceInfo: resolve()}
						}
					} else {
						opts.ResourceSourceInfoProvider = resolve
					}
					m := reloadTestMode(opts)
					m.uiTaskCh = make(chan func(), 1)
					m.resourceSourceInfo = old
					m.editorContainer = tui.NewContainer(m.editor)
					m.tuiInst.SetFocus(m.editor)
					done := make(chan error, 1)
					go func() { done <- m.buildSlashContext(ctx).Reload() }()
					<-entered

					applied := false
					cause := errors.New("reload owner stopped")
					if err := m.postToMain(t.Context(), func() {
						applied = true
						m.tuiInst.Render()
						if cancelOwner {
							cancel(cause)
						}
					}); err != nil {
						t.Fatal(err)
					}
					synctest.Wait()
					if !applied {
						t.Error("pending resource resolution blocked UI owner work")
					}
					input, routeDone := m.modalRoute()
					if input == nil {
						t.Error("pending resource resolution did not retain a modal input route")
					} else {
						consumed := false
						go func() {
							select {
							case input <- []byte("ignored during reload"):
								consumed = true
							case <-routeDone:
							}
						}()
						synctest.Wait()
						if !consumed {
							t.Error("pending resource resolution stopped draining input")
						}
					}
					if !maps.Equal(m.resourceSourceInfo, old) || !slices.Equal(order, []string{"resolve"}) {
						t.Errorf("reload published resources or advanced before resolution: infos=%v order=%v", m.resourceSourceInfo, order)
					}
					if m.tuiInst.GetFocusedComponent() == m.editor {
						t.Error("pending resolution restored editor focus")
					}
					select {
					case err := <-done:
						t.Fatalf("reload returned before resolution joined: %v", err)
					default:
					}
					// Release even on the red path, so the regression fails without hanging.
					close(release)
					err := <-done
					if cancelOwner && applied {
						if !errors.Is(err, cause) || !maps.Equal(m.resourceSourceInfo, old) || !slices.Equal(order, []string{"resolve"}) {
							t.Errorf("cancelled reload published or advanced: err=%v infos=%v order=%v", err, m.resourceSourceInfo, order)
						}
					} else if err != nil || !maps.Equal(m.resourceSourceInfo, fresh) || !slices.Equal(order, []string{"resolve", "stage"}) {
						t.Errorf("completed reload: err=%v infos=%v order=%v", err, m.resourceSourceInfo, order)
					}
					if m.tuiInst.GetFocusedComponent() != m.editor || m.modalInputCh != nil {
						t.Error("reload retained its focus or input route after joining")
					}
				})
			})
		}
	}
}
