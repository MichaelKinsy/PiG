package subprocess

import (
	"context"
	"encoding/json"
	"net"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// settleUI is a dialog UI whose answers a test controls: each Select hands the test the settle function its call carries
// (extension.WithDialogSettle, which RPC mode runs on the stdin line that answers the dialog), then waits to be answered.
type settleUI struct {
	extension.UIContext
	settles chan titledSettle
	mu      sync.Mutex
	answers map[string]chan struct{}
}

type titledSettle struct {
	title  string
	settle func()
}

func (*settleUI) ReportsDialogInitiation() bool { return true }

func (u *settleUI) answer(title string) chan struct{} {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.answers[title] == nil {
		u.answers[title] = make(chan struct{})
	}
	return u.answers[title]
}

func (u *settleUI) Select(ctx context.Context, title string, _ []string, _ extension.ExtensionUIDialogOptions) (string, error) {
	extension.CallInitiated(ctx)
	u.settles <- titledSettle{title: title, settle: extension.DialogSettleFromContext(ctx)}
	<-u.answer(title)
	return title, nil
}

// Pi resolves a dialog's promise on the stdin line that answers it, so dialogs answered back to back continue in the order of the
// answers and an extension receives their results in that order. The host sends the replies of a connection's dialogs in the order
// the UI reported their answers, not in the order the goroutines that wait in the dialogs reach the send: here the second dialog
// is answered first at the UI's end, but its answer was read second, and its reply waits for the first dialog's.
func TestDialogRepliesFollowTheOrderTheirAnswersArrived(t *testing.T) {
	ui := &settleUI{UIContext: extension.NoopUIContext, settles: make(chan titledSettle, 2), answers: map[string]chan struct{}{}}
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	h.SetUIBridge(bridge)
	host, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := NewConn("dialog-order", host)
	conn.Start(t.Context())
	defer func() { _ = conn.Close("test done") }()
	parked := make(chan struct{}, 2)
	conn.dialogOrder.SetParkedHook(func() {
		select {
		case parked <- struct{}{}:
		default:
		}
	})

	replies := make(chan string, 8)
	go func() {
		for {
			env, err := tryReadFramed(peer)
			if err != nil {
				return
			}
			if env.Type == MsgCallResult {
				replies <- env.ID
			}
		}
	}()

	me := withConn(&managedExt{config: ExtConfig{Name: "dialog-order"}, host: h}, conn)
	lanes := newCallLanes()
	for id, title := range map[string]string{"c1": "First?", "c2": "Second?"} {
		args, _ := json.Marshal(map[string]any{"title": title, "options": []string{"a"}})
		h.queueCall(me, me.connection(), lanes, id, &CallPayload{Method: "ui.select", Args: args})
	}
	settles := map[string]func(){}
	for range 2 {
		s := <-ui.settles
		settles[s.title] = s.settle
	}
	if settles["First?"] == nil || settles["Second?"] == nil {
		t.Fatal("a dialog call carries no settle function")
	}
	// The reader reads the answer of First? and then the answer of Second?.
	settles["First?"]()
	settles["Second?"]()
	// The goroutine of Second? is the first to return from its dialog.
	close(ui.answer("Second?"))
	<-parked
	close(ui.answer("First?"))
	var got []string
	for range 2 {
		got = append(got, <-replies)
	}
	if want := []string{"c1", "c2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("replies in order %v, want %v (the order the answers arrived)", got, want)
	}
}
