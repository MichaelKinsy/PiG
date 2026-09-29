package subprocess

import (
	"encoding/binary"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestNodeProviderSocketRejectsBadFrameBeforeNextEnvelope(t *testing.T) {
	module := filepath.Join(findModuleRoot(t), "coding", "extension", "host", "subprocess", "runtime-node", "provider-socket.mjs")
	for _, kind := range []string{"oversized", "empty", "invalid-json", "null"} {
		t.Run(kind, func(t *testing.T) {
			listener, path, err := ListenExtension(filepath.Join(t.TempDir(), "s"), true)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = listener.Close() }()
			body := map[string]string{"empty": "", "invalid-json": "}", "null": "null"}[kind]
			wire := make([]byte, 4)
			if kind == "oversized" {
				binary.BigEndian.PutUint32(wire, MaxFrameSize+1)
			} else {
				binary.BigEndian.PutUint32(wire, uint32(len(body)))
				wire = append(wire, body...)
			}
			next := `{"type":"notify","notify":{"method":"must-not-arrive"}}`
			header := make([]byte, 4)
			binary.BigEndian.PutUint32(header, uint32(len(next)))
			wire = append(wire, header...)
			wire = append(wire, next...)
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
					_, err = conn.Write(wire)
				}
				finished <- err
			}()
			script := `import assert from "node:assert/strict";
import {pathToFileURL} from "node:url";
import {once} from "node:events";
const {ProviderSocket} = await import(pathToFileURL(process.argv[1]));
let expected = "frame too large: " + process.argv[5];
if (process.argv[3] !== "oversized") {
 try { const value=JSON.parse(process.argv[4]); void value.type; } catch(error) { expected=error.message; }
}
const socket = await ProviderSocket.connect(process.argv[2]);
let error;
const envelopes=[];
socket.on("error", value=>{error=value.message});
socket.on("envelope", value=>envelopes.push(value));
const exited=once(socket.worker,"exit");
socket.write(Buffer.from("s"));
// Worker exit and MessagePort delivery use different queues. Force exit handling first, after the real worker has posted its error and finished.
while (!Atomics.load(socket.state,3)) {
 const sequence=Atomics.load(socket.state,0);
 if (!Atomics.load(socket.state,3)) Atomics.wait(socket.state,0,sequence);
}
socket.worker.emit("exit",0);
await exited;
assert.equal(error,expected);
assert.deepEqual(envelopes,[],"bad frame must close before a later envelope is delivered");
`
			cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", script, module, path, kind, body, strconv.Itoa(MaxFrameSize+1))
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("frame rejection: %v\n%s", err, output)
			}
			if err := <-finished; err != nil {
				t.Fatal(err)
			}
		})
	}
}
