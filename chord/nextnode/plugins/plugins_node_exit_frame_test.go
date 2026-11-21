package plugins

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestFramedPortDeliversAFrameSentRightBeforeProcessExit exercises the real listener (listenNode), a real Node subprocess
// importing the extracted runtime's port.mjs, and Go's framedPort reader: the subprocess connects, sends one link frame
// via FramedPort.send and calls process.exit(7) in the same turn without waiting for the write callback. Go must receive
// that frame whole, then observe the channel end, and the subprocess must exit with status 7.
//
// On Linux a Unix domain socket completes both writes synchronously into the kernel, so this passes before and after the
// single-write cure; on Windows named pipes, two socket.write calls leave the payload in Node's Writable buffer and lose
// the frame on process.exit(7), so this test fails before the cure and passes after.
func TestFramedPortDeliversAFrameSentRightBeforeProcessExit(t *testing.T) {
	node, err := discoverNode(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := extractNode(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	portURL := pcJSON(t, fileURL(filepath.Join(tree, "port.mjs")))

	const runs = 20
	for i := range runs {
		listener, address, cleanup, err := listenNode()
		if err != nil {
			t.Fatal(err)
		}

		// Patterned payload of a few hundred bytes, distinct per iteration.
		want := bytes.Repeat([]byte{byte(0x20 + i), byte(0xA5 ^ i), byte(i), 0x5A}, 80)

		accepted := make(chan net.Conn, 1)
		acceptErr := make(chan error, 1)
		go func() {
			conn, err := listener.Accept()
			if err != nil {
				acceptErr <- err
				return
			}
			accepted <- conn
		}()

		script := fmt.Sprintf(`
import net from "node:net";
import { FramedPort } from %s;
const address = process.argv[1];
const iter = Number(process.argv[2]);
const payload = new Uint8Array(320);
for (let j = 0; j < payload.length; j += 4) {
	payload[j] = (0x20 + iter) & 0xff;
	payload[j + 1] = (0xa5 ^ iter) & 0xff;
	payload[j + 2] = iter & 0xff;
	payload[j + 3] = 0x5a;
}
const socket = net.connect(address, () => {
	const port = new FramedPort(socket);
	port.send(payload, () => {});
	process.exit(7);
});
socket.on("error", (err) => {
	console.error(err);
	process.exit(9);
});
`, portURL)

		cmd := exec.CommandContext(t.Context(), node, "--input-type=module", "--eval", script, address, fmt.Sprintf("%d", i))
		if err := cmd.Start(); err != nil {
			_ = listener.Close()
			cleanup()
			t.Fatalf("run %d: start node: %v", i, err)
		}

		var conn net.Conn
		select {
		case conn = <-accepted:
		case err := <-acceptErr:
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			_ = listener.Close()
			cleanup()
			t.Fatalf("run %d: accept: %v", i, err)
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			_ = listener.Close()
			cleanup()
			t.Fatalf("run %d: timed out waiting for node to connect", i)
		}

		port := newFramedPort(conn, func([]byte) {})
		var got [][]byte
		port.OnMessage(func(message any) {
			if frame, ok := message.([]byte); ok {
				got = append(got, append([]byte(nil), frame...))
			}
		})
		disconnected := make(chan struct{})
		port.OnDisconnect(func() { close(disconnected) })
		go port.read()

		waitErr := cmd.Wait()
		select {
		case <-disconnected:
		case <-time.After(10 * time.Second):
			_ = conn.Close()
			_ = listener.Close()
			cleanup()
			t.Fatalf("run %d: channel did not end after process exited", i)
		}
		<-port.done
		_ = listener.Close()
		cleanup()

		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) || exitErr.ExitCode() != 7 {
			t.Fatalf("run %d: node exit = %v, want exit status 7", i, waitErr)
		}
		if len(got) != 1 {
			t.Fatalf("run %d: received %d frames before channel end, want 1 (cut frame dropped on EOF)", i, len(got))
		}
		if !bytes.Equal(got[0], want) {
			t.Fatalf("run %d: frame payload mismatch: got %d bytes, want %d bytes", i, len(got[0]), len(want))
		}
	}
}
