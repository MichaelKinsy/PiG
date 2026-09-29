package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/5303-bash-output-truncation.test.ts:39
// "captures output emitted after exit while a descendant holds stdout open".
func TestWaitForStdioIdleRearmsOnEveryChunk(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		operations := portBashOperations(func(_ context.Context, _, _ string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
			activity := make(chan struct{})
			result := make(chan bool, 1)
			opts.OnData([]byte("HEAD\n"))
			// The fixture's shell exits with code zero; its pipe remains open. The
			// production wait gate controls when operations may return that code.
			go func() { result <- waitForStdioIdle(make(chan struct{}), activity) }()
			for index := 1; index <= 6; index++ {
				time.Sleep(50 * time.Millisecond)
				opts.OnData(fmt.Appendf(nil, "TICK%d\n", index))
				select {
				case activity <- struct{}{}:
				case <-result:
					t.Fatal("the wait resolved while output was still arriving every 50ms")
				}
			}
			time.Sleep(99 * time.Millisecond)
			synctest.Wait()
			select {
			case <-result:
				t.Fatal("the wait resolved 99ms after the last chunk, before the grace elapsed")
			default:
			}
			time.Sleep(time.Millisecond)
			synctest.Wait()
			select {
			case closed := <-result:
				if closed {
					t.Fatal("the wait reported a closed pipe; want release by the idle grace")
				}
			default:
				t.Fatal("the wait did not resolve once the grace elapsed after the last chunk")
			}
			return BashOperationsResult{ExitCode: new(0)}, nil
		})
		result, err := ExecuteBashWithOperations(t.Context(), "after-exit", t.TempDir(), operations, BashExecOptions{})
		if err != nil || result.ExitCode == nil || *result.ExitCode != 0 {
			t.Fatalf("result = %+v, %v", result, err)
		}
		if !strings.Contains(result.Output, "HEAD") || !strings.Contains(result.Output, "TICK6") {
			t.Fatalf("output = %q", result.Output)
		}
	})
}

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/5303-bash-output-truncation.test.ts:66
// "resolves after the grace when a descendant holds stdout open but stays quiet".
func TestWaitForStdioIdleReleasesAQuietHeldPipe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		operations := portBashOperations(func(_ context.Context, _, _ string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
			result := make(chan bool, 1)
			opts.OnData([]byte("DONE\n"))
			go func() { result <- waitForStdioIdle(make(chan struct{}), make(chan struct{})) }()
			time.Sleep(99 * time.Millisecond)
			synctest.Wait()
			select {
			case <-result:
				t.Fatal("the wait resolved before the grace elapsed")
			default:
			}
			time.Sleep(time.Millisecond)
			synctest.Wait()
			select {
			case closed := <-result:
				if closed {
					t.Fatal("the wait reported a closed pipe; want release by the idle grace")
				}
			default:
				t.Fatal("the wait did not resolve after the grace")
			}
			return BashOperationsResult{ExitCode: new(0)}, nil
		})
		result, err := ExecuteBashWithOperations(t.Context(), "after-exit", t.TempDir(), operations, BashExecOptions{})
		if err != nil || result.ExitCode == nil || *result.ExitCode != 0 {
			t.Fatalf("result = %+v, %v", result, err)
		}
	})
}
