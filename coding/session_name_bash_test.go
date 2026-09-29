package coding

import (
	"context"
	"reflect"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

type sessionNameBashOperations struct {
	release <-chan struct{}
}

func (ops sessionNameBashOperations) Exec(ctx context.Context, _, _ string, opts extension.BashOperationsExecOptions) (extension.BashOperationsResult, error) {
	opts.OnData([]byte("verifier"))
	select {
	case <-ops.release:
		return extension.BashOperationsResult{ExitCode: new(0)}, nil
	case <-ctx.Done():
		return extension.BashOperationsResult{}, ctx.Err()
	}
}

// Pi 0.87.1 agent-session.ts:3458-3493,3559-3563 starts the name handler's prefix but does not await its executeBash Promise. Output and later name events must publish while Bash is pending; completion still records its result and Close cancels and joins it.
func TestSessionNameHandlerBashDoesNotBlockPublication(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		name := "complete"
		if cancel {
			name = "cancel"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				release := make(chan struct{})
				results := make(chan BashResult, 1)
				second := make(chan struct{})
				var s *Session
				h := newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Handlers: map[string][]extension.HandlerFn{
					"session_info_changed": {func(args ...any) (any, error) {
						if args[0].(extension.SessionInfoChangedEvent).Name == "second" {
							close(second)
							return nil, nil
						}
						result, err := s.ExecuteBashWithOperations(args[1].(context.Context), "printf verifier", false, nil, sessionNameBashOperations{release: release}, nil)
						if err != nil {
							t.Errorf("ExecuteBashWithOperations: %v", err)
						}
						results <- result
						return nil, err
					}},
				}}})
				s = h.session
				set := make(chan error, 2)
				go func() { set <- s.SetSessionName("first") }()
				synctest.Wait()
				select {
				case err := <-set:
					if err != nil {
						t.Fatal(err)
					}
				default:
					t.Error("SetSessionName awaited Bash completion")
				}
				var deltas []string
				for _, event := range h.events {
					if update, ok := event.(agent.BashExecutionUpdateEvent); ok {
						deltas = append(deltas, update.Delta)
					}
				}
				if !reflect.DeepEqual(deltas, []string{"verifier"}) {
					t.Errorf("Bash output blocked behind its own name handler: %q", deltas)
				}
				go func() { set <- s.SetSessionName("second") }()
				synctest.Wait()
				select {
				case <-second:
				default:
					t.Error("later name notification blocked behind Bash")
				}
				select {
				case <-results:
					t.Error("Bash returned before its operation completed")
				default:
				}
				cancelled := cancel || t.Failed()
				if cancelled {
					if err := s.Close(); err != nil {
						t.Fatal(err)
					}
				} else {
					close(release)
				}
				synctest.Wait()
				select {
				case result := <-results:
					if result.Output != "verifier" || result.Cancelled != cancelled {
						t.Errorf("Bash result = %+v", result)
					}
					if !result.Cancelled && (result.ExitCode == nil || *result.ExitCode != 0) {
						t.Errorf("Bash exit code = %v", result.ExitCode)
					}
				default:
					t.Error("Bash handler did not settle")
				}
				if bashIsRunning(s) {
					t.Error("settled Bash retained its cancellation registration")
				}
				if !t.Failed() {
					assertLastBashRole(t, s)
				}
			})
		})
	}
}
