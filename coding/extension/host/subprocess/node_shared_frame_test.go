package subprocess

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The host sends each model registry publication to every member of a Node cell. One IO worker reads those identical large frames for every socket of the process, hands them to the main thread as shared bytes with one identity, and the main thread decodes a frame the runtime accepts as shared once: every socket receives the same envelope. Any other large frame still decodes once per socket, so its value stays each receiver's own. The worker reads a repeat by comparison as it arrives: the second socket's two large frames allocate no shared buffer.
func TestNodeProviderSocketsShareARepeatedLargeFrame(t *testing.T) {
	listener, path, err := ListenExtension(filepath.Join(shortSockDir(t), "s"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	frame := func(value any) []byte {
		t.Helper()
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return append(binary.BigEndian.AppendUint32(nil, uint32(len(body))), body...)
	}
	items := make([]map[string]any, 0, 2000)
	for i := range 2000 {
		items = append(items, map[string]any{"id": i, "name": strings.Repeat("n", i%40)})
	}
	wire := append(append(
		frame(map[string]any{"type": "notify", "notify": map[string]any{"method": "shared", "args": map[string]any{"items": items}}}),
		frame(map[string]any{"type": "notify", "notify": map[string]any{"method": "private", "args": map[string]any{"items": items, "private": true}}})...),
		frame(map[string]any{"type": "notify", "notify": map[string]any{"method": "small"}})...)
	var served sync.WaitGroup
	failures := make(chan error, 3)
	served.Go(func() {
		var conns sync.WaitGroup
		defer conns.Wait()
		for range 2 {
			conn, err := listener.Accept()
			if err != nil {
				failures <- err
				return
			}
			conns.Go(func() {
				defer func() { _ = conn.Close() }()
				var start [1]byte
				if _, err := io.ReadFull(conn, start[:]); err != nil {
					failures <- err
					return
				}
				if _, err := conn.Write(wire); err != nil {
					failures <- err
				}
			})
		}
	})
	module := filepath.Join(findModuleRoot(t), "coding", "extension", "host", "subprocess", "runtime-node", "provider-socket.mjs")
	dir := t.TempDir()
	trace := filepath.Join(dir, "allocations.mjs")
	allocations := filepath.Join(dir, "allocations")
	write(t, trace, `import {writeFileSync} from "node:fs";
import {isMainThread} from "node:worker_threads";
if (!isMainThread) {
  let large = 0;
  globalThis.SharedArrayBuffer = new Proxy(SharedArrayBuffer, {construct(target, args) { if (args[0] >= 64 * 1024) large++; return Reflect.construct(target, args); }});
  process.on("exit", () => writeFileSync(process.env.SHARED_FRAME_LOG, String(large)));
}
`)
	script := `import assert from "node:assert/strict";
import {createRequire, syncBuiltinESMExports} from "node:module";
import {pathToFileURL} from "node:url";
import {once} from "node:events";
const threads = createRequire(import.meta.url)("node:worker_threads");
const OriginalWorker = threads.Worker;
threads.Worker = class extends OriginalWorker {
 constructor(url, options) {super(url, {...options, execArgv: ["--import", pathToFileURL(process.argv[3]).href]});}
};
syncBuiltinESMExports();
const {ProviderSocket, shareDecodedFrames} = await import(pathToFileURL(process.argv[1]));
shareDecodedFrames(envelope => envelope.notify?.method === "shared");
const sockets = [await ProviderSocket.connect(process.argv[2]), await ProviderSocket.connect(process.argv[2])];
const worker = sockets[0].worker;
assert.equal(sockets[1].worker, worker, "one IO worker serves every socket of the process");
const exited = once(worker, "exit");
const frames = sockets.map(() => []);
const received = sockets.map(() => []);
const complete = sockets.map(() => Promise.withResolvers());
sockets.forEach((socket, i) => {
 socket.port.on("message", message => { if (message.kind === "envelope") frames[i].push(message.frame instanceof SharedArrayBuffer ? message.id : "text"); });
 socket.on("envelope", envelope => { if (received[i].push(envelope) === 3) complete[i].resolve(); });
});
const closed = sockets.map(socket => once(socket, "close"));
// The second socket's frames arrive after the first socket's are complete, as a publication's copies follow each other.
sockets[0].write(Buffer.from("s"));
await complete[0].promise;
sockets[1].write(Buffer.from("s"));
await Promise.all(closed);
for (const envelopes of received) assert.deepEqual(envelopes.map(envelope => envelope.notify.method), ["shared", "private", "small"]);
assert.deepEqual(frames[0], frames[1], "a repeated large frame keeps its identity across sockets");
assert.equal(frames[0][2], "text", "a small frame stays text");
assert.notEqual(frames[0][0], frames[0][1]);
assert.equal(received[0][0], received[1][0], "a shared frame decodes once for every socket");
assert.notEqual(received[0][1], received[1][1], "any other frame decodes for each socket");
assert.deepEqual(received[0][1], received[1][1]);
await exited;
`
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", script, module, path, trace)
	cmd.Env = append(os.Environ(), "SHARED_FRAME_LOG="+allocations)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("shared frames: %v\n%s", err, output)
	}
	count, err := os.ReadFile(allocations)
	if err != nil {
		t.Fatal(err)
	}
	if string(count) != "2" {
		t.Errorf("the IO worker allocated %s shared frame buffers for two sockets' copies of two large frames, want 2: a repeat is read by comparison", count)
	}
	served.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
}
