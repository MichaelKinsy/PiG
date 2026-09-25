package codingagent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

type navigateRecordingHandle struct {
	*recordingCompactHandle
	target    string
	summarize bool
}

func (h *navigateRecordingHandle) NavigateTreeHandle(_ context.Context, target string, summarize bool, _ string) (NavigateTreeResult, error) {
	h.target, h.summarize = target, summarize
	return NavigateTreeResult{Cancelled: target == "cancel"}, nil
}

// In interactive mode ctx.navigateTree navigates the Session and reports
// cancellation (interactive-mode.ts commandContextActions.navigateTree), and
// the binding survives an extension reload's runner swap.
func TestInteractiveExtensionNavigateTreeIsBound(t *testing.T) {
	handle := &navigateRecordingHandle{recordingCompactHandle: &recordingCompactHandle{}}
	m := &InteractiveMode{opts: InteractiveOptions{SessionHandle: handle}, newRunner: inproc.NewRunner(nil, t.TempDir())}
	m.wireInprocContextActions()
	result, err := m.newRunner.CreateCommandContext().NavigateTree("entry-1", &extension.NavigateTreeOptions{Summarize: true})
	if err != nil || result.Cancelled || handle.target != "entry-1" || !handle.summarize {
		t.Fatalf("NavigateTree = %+v, %v; session got %q summarize=%v", result, err, handle.target, handle.summarize)
	}
	result, err = m.newRunner.CreateCommandContext().NavigateTree("cancel", nil)
	if err != nil || !result.Cancelled {
		t.Fatalf("cancelled navigation = %+v, %v; want cancelled", result, err)
	}
}

type blockingNavigateInitiationHandle struct {
	*recordingCompactHandle
	entered atomic.Bool
	release chan struct{}
}

func (h *blockingNavigateInitiationHandle) NavigateTreeHandle(ctx context.Context, _ string, _ bool, _ string) (NavigateTreeResult, error) {
	h.entered.Store(true)
	extension.CallInitiated(ctx)
	<-h.release
	return NavigateTreeResult{}, nil
}

// The Session action owns the Promise initiation boundary. The interactive
// adapter must pass its call context through instead of releasing the ordered
// lane before Session navigation has been admitted, while completion remains
// independent after admission.
func TestInteractiveExtensionNavigateInitiationFollowsSessionAdmission(t *testing.T) {
	handle := &blockingNavigateInitiationHandle{recordingCompactHandle: &recordingCompactHandle{}, release: make(chan struct{})}
	m := &InteractiveMode{opts: InteractiveOptions{SessionHandle: handle}}
	initiated := make(chan struct{})
	ctx := extension.WithCallInitiation(t.Context(), func() {
		if !handle.entered.Load() {
			t.Error("navigateTree lane released before Session action entry")
		}
		close(initiated)
	})
	done := make(chan error, 1)
	go func() {
		_, err := m.extensionNavigateTreeContext(ctx, "target", nil)
		done <- err
	}()
	select {
	case <-initiated:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("Session navigation never reported admission")
	}
	select {
	case err := <-done:
		t.Fatalf("Session navigation completed before release: %v", err)
	default:
	}
	close(handle.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
