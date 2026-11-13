//go:build unix

package workers

import (
	"encoding/binary"
	"encoding/json"
	"net"
	"os"
	"reflect"
	"sync"
	"syscall"
	"testing"
	"testing/synctest"
)

// A send that fails does not close the channel under its reader. Node tells a failed `send` to its callback (or as an
// `error` event) and goes on reading: what the other end wrote before it closed is still emitted, then `disconnect`. The
// channel has no Pi file (workers_channel.go), so neither has this test; the order is Node's.

// channelPair is a channel on one end of a socket pair and the raw other end.
func channelPair(t *testing.T) (*channel, net.Conn) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	ends := make([]net.Conn, 2)
	for index, fd := range fds {
		syscall.CloseOnExec(fd)
		file := os.NewFile(uintptr(fd), "ipc")
		ends[index], err = net.FileConn(file)
		_ = file.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	return newChannel(ends[0]), ends[1]
}

// controlRecord is the record of one control message.
func controlRecord(text string) []byte {
	payload, _ := json.Marshal(text)
	record := []byte{recordControl, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(record[1:], uint32(len(payload)))
	return append(record, payload...)
}

// The other end wrote two messages and closed. While the reader is held telling the first, a send fails (nobody reads
// it): its callback is told, a later send is refused, and the reader still tells the second message before the disconnect.
func TestAChannelTellsWhatTheOtherEndWroteBeforeItClosedWhenASendFailedMeanwhile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ours, other := channelPair(t)
		var mu sync.Mutex
		var told []string
		var sendErrors []error
		held, release, gone := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var once sync.Once
		ours.OnMessage(func(message any) {
			once.Do(func() {
				close(held)
				<-release
			})
			mu.Lock()
			defer mu.Unlock()
			told = append(told, string(message.(json.RawMessage)))
		})
		ours.OnDisconnect(func() {
			mu.Lock()
			told = append(told, "disconnect")
			mu.Unlock()
			close(gone)
		})
		if _, err := other.Write(append(controlRecord("first"), controlRecord("second")...)); err != nil {
			t.Fatal(err)
		}
		if err := other.Close(); err != nil {
			t.Fatal(err)
		}
		ours.start()
		<-held
		_, _ = ours.Send([]byte("late"), func(err error) {
			mu.Lock()
			defer mu.Unlock()
			sendErrors = append(sendErrors, err)
		})
		// The writer met the closed end.
		synctest.Wait()
		mu.Lock()
		failed := len(sendErrors) == 1 && sendErrors[0] != nil
		mu.Unlock()
		if !failed {
			t.Errorf("the failed send's callback: %v, want one error", sendErrors)
		}
		if taken, _ := ours.Send([]byte("later"), nil); taken {
			t.Error("a send after a failed one was taken")
		}
		close(release)
		<-gone
		mu.Lock()
		defer mu.Unlock()
		if want := []string{`"first"`, `"second"`, "disconnect"}; !reflect.DeepEqual(told, want) {
			t.Errorf("told = %q, want %q: what the other end wrote is told before the disconnect", told, want)
		}
	})
}
