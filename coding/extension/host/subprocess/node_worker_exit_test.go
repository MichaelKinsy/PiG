package subprocess

import (
	"encoding/binary"
	"io"
	"os/exec"
	"path/filepath"
	"testing"
)

// A worker exit may reach the main thread before messages on its transferred port. The socket must deliver all complete frames before its close event.
func TestNodeProviderSocketDrainsPendingEnvelopesBeforeWorkerExit(t *testing.T) {
	listener, path, err := ListenExtension(filepath.Join(t.TempDir(), "s"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	finished := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			finished <- err
			return
		}
		defer func() { _ = conn.Close() }()
		var start [1]byte
		if _, err = io.ReadFull(conn, start[:]); err == nil {
			for _, body := range []string{`{"type":"notify","notify":{"method":"first"}}`, `{"type":"notify","notify":{"method":"last"}}`} {
				wire := make([]byte, 4, 4+len(body))
				binary.BigEndian.PutUint32(wire, uint32(len(body)))
				if _, err = conn.Write(append(wire, body...)); err != nil {
					break
				}
			}
		}
		finished <- err
	}()
	module := filepath.Join(findModuleRoot(t), "coding", "extension", "host", "subprocess", "runtime-node", "provider-socket.mjs")
	script := `import assert from "node:assert/strict";
import {pathToFileURL} from "node:url";
import {once} from "node:events";
const {ProviderSocket}=await import(pathToFileURL(process.argv[1]));
const socket=await ProviderSocket.connect(process.argv[2]);
const events=[];
socket.on("envelope",value=>events.push(value.notify.method));
socket.on("close",()=>events.push("close"));
socket.on("error",error=>{throw error});
const exited=once(socket.worker,"exit");
socket.write(Buffer.from("s"));
while (!Atomics.load(socket.state,3)) {
 const sequence=Atomics.load(socket.state,0);
 if (!Atomics.load(socket.state,3)) Atomics.wait(socket.state,0,sequence);
}
socket.worker.emit("exit",0);
await exited;
assert.deepEqual(events,["first","last","close"]);
socket.pump();
assert.deepEqual(events,["first","last","close"],"drain and close must not replay events");
`
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", script, module, path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("worker exit drain: %v\n%s", err, output)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}
