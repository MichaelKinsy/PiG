package codingagent

import (
	"context"
	"errors"
	"io"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi 0.87.1 interactive-mode.ts:4138-4157,4231-4246 shuts down independently of selector focus.
func TestModalSelectorsReleaseOnShutdown(t *testing.T) {
	for _, action := range []string{"signal", "cancel", "request", "input-error"} {
		for _, selector := range []string{"scoped-models", "settings", "model", "login", "api-key", "component", "custom"} {
			t.Run(selector+"/"+action, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					m, _ := newExtensionDialogProbe(t)
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					m.runCtx = ctx
					m.opts.AgentDir = t.TempDir()
					m.inputErrCh = make(chan error, 1)
					customDone := make(chan struct{})
					done := make(chan struct{})
					go func() {
						defer close(done)
						switch selector {
						case "scoped-models":
							selection, ids := newScopedModelsSelection(nil, nil, nil)
							m.runModalScopedModels(tui.NewScopedModelsList(tui.ScopedModelsConfig{EnabledModelIDs: ids}), nil, selection)
						case "settings":
							m.runModalSettingsSelector(func(done func()) *SettingsSelectorComponent {
								return NewSettingsSelectorComponent(SettingsConfig{}, SettingsCallbacks{OnCancel: done})
							})
						case "model":
							m.runModelSelectorInput(ctx, tui.NewStaticModelSelectorComponent("Model", nil, nil, ""))
						case "login":
							m.runEditorSlotLoginDialog(tui.NewLoginDialogComponent(nil, "Provider", nil, ""), nil)
						case "api-key":
							_ = m.runAPIKeyLogin(tui.OAuthProvider{ID: "openai", Name: "OpenAI"})
						case "custom":
							m.runEditorSlotCustom(tui.NewSettingsList(nil, 10, tui.GetSettingsListTheme(), nil, nil, tui.SettingsListOptions{EnableSearch: true}), nil, nil, customDone)
						case "component":
							sel := tui.NewSelectSubmenu("Choice", "", nil, "")
							m.runEditorSlotComponent(sel, sel.HandleInput, sel.Done)
						}
					}()
					synctest.Wait()
					input, _ := m.modalRoute()
					if input == nil {
						t.Fatal("selector never acquired input")
					}
					switch action {
					case "signal":
						m.ShutdownFromSignal()
						cancel()
					case "cancel":
						cancel()
					case "input-error":
						m.inputErrCh <- io.EOF
					case "request":
						m.requestShutdown()
					}
					synctest.Wait()
					select {
					case <-done:
					default:
						t.Error("shutdown left the owner loop blocked in the selector")
						// Release the failing implementation without leaking its goroutine.
						if selector == "custom" {
							close(customDone)
						} else {
							input <- []byte("\x1b")
						}
						<-done
					}
					if action == "input-error" && !errors.Is(m.inputLoopErr, io.EOF) {
						t.Errorf("input error lost: %v", m.inputLoopErr)
					}
					if route, _ := m.modalRoute(); route != nil {
						t.Error("selector retained the input route after shutdown")
					}
				})
			})
		}
	}
}

type shutdownSummaryHandle struct {
	InteractiveSessionHandle
	cancelled, release chan struct{}
}

func (h *shutdownSummaryHandle) SummarizeForBugReport(ctx context.Context, _ string) (string, error) {
	<-ctx.Done()
	close(h.cancelled)
	<-h.release
	return "", ctx.Err()
}

func TestModalSummaryShutdownCancelsAndJoinsWork(t *testing.T) {
	for _, action := range []string{"cancel", "request"} {
		t.Run(action, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m, _ := newExtensionDialogProbe(t)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				m.runCtx = ctx
				h := &shutdownSummaryHandle{cancelled: make(chan struct{}), release: make(chan struct{})}
				m.opts.SessionHandle = h
				done := make(chan struct{})
				go func() {
					defer close(done)
					_, cancelled, err := m.summarizeForBugReport("model", "")
					if !cancelled || err != nil {
						t.Errorf("summary cancellation = %v, %v", cancelled, err)
					}
				}()
				synctest.Wait()
				if action == "cancel" {
					cancel()
				} else {
					m.requestShutdown()
				}
				synctest.Wait()
				select {
				case <-h.cancelled:
				default:
					t.Error("shutdown did not cancel summary work")
					input, _ := m.modalRoute()
					input <- []byte("\x1b")
					synctest.Wait()
				}
				select {
				case <-done:
					t.Error("shutdown returned before joining summary work")
				default:
				}
				close(h.release)
				<-done
			})
		})
	}
}

func TestModalTreeShutdownRequestCancelsWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := statusBorderMode(t, true)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		m.runCtx = ctx
		h := &lifecycleNavigationHandle{events: make(chan agent.AgentEvent, 1)}
		m.opts.SessionHandle, m.eventCh = h, h.events
		h.navigate = func(ctx context.Context) (NavigateTreeResult, error) {
			<-ctx.Done()
			return NavigateTreeResult{}, ctx.Err()
		}
		done := make(chan error, 1)
		go func() {
			_, err := m.navigateTree(ctx, "target", true, "")
			done <- err
		}()
		synctest.Wait()
		m.requestShutdown()
		synctest.Wait()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("navigation shutdown = %v", err)
			}
		default:
			t.Error("shutdown request did not cancel navigation work")
			cancel()
			<-done
		}
	})
}
