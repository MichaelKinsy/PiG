package plugins

import (
	"net"
	"testing"
	"time"
)

// TestFramedPortRefusesAFrameTheReaderRejects pins that write sends no frame whose length the other end's read ends the channel on
// (maxFrame), and does not wrap the four-byte length: a payload of 4 GiB would be written as a frame of length 1.
func TestFramedPortRefusesAFrameTheReaderRejects(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()
	port := newFramedPort(client, func([]byte) {})

	// One more byte than maxFrame allows with the kind byte: no bytes may reach the connection.
	if err := port.write(frameLink, make([]byte, maxFrame)); err == nil {
		t.Fatal("write accepted a frame longer than maxFrame")
	}
	_ = server.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if n, _ := server.Read(make([]byte, 1)); n != 0 {
		t.Fatalf("a refused frame still wrote %d bytes", n)
	}

	// The largest frame the reader accepts is written whole.
	done := make(chan error, 1)
	go func() { done <- port.write(frameLink, make([]byte, maxFrame-1)) }()
	header := make([]byte, 5)
	_ = server.SetReadDeadline(time.Time{})
	if _, err := server.Read(header); err != nil {
		t.Fatal(err)
	}
	if got := uint32(header[0])<<24 | uint32(header[1])<<16 | uint32(header[2])<<8 | uint32(header[3]); got != maxFrame {
		t.Fatalf("frame length = %d, want %d", got, maxFrame)
	}
	_ = server.Close()
	<-done
}

// TestFramedPortWritesEachFrameInOneSocketWrite pins that the Node driver's FramedPort writes a frame's 5-byte header and
// payload in one socket.write call. On Windows named pipes libuv's uv_try_write always returns UV_EAGAIN so the first write
// stays asynchronous inside Writable while it is in flight; a second socket.write is buffered in JS memory until the first
// completes, so a process.exit() right after the send hands the kernel only the 5-byte header and loses the frame.
func TestFramedPortWritesEachFrameInOneSocketWrite(t *testing.T) {
	runVendored(t, func(pi func(string) string) string {
		return `
const { Writable } = await import("node:stream");
const { FramedPort } = await import(` + pi("../../../port.mjs") + `);

// A Writable whose _write never completes synchronously, matching a Windows named pipe (libuv uv_try_write -> UV_EAGAIN).
// Only chunks handed to _write reach the kernel before an immediate process exit; chunks buffered in Writable are lost.
function asyncSocket(highWaterMark = 64 * 1024) {
	let writeCalls = 0;
	let endCalls = 0;
	const writes = [];
	const pending = [];
	let ended = false;
	const socket = new Writable({
		highWaterMark,
		write(chunk, _encoding, callback) {
			writes.push(Buffer.from(chunk));
			pending.push(callback);
		},
		final(callback) {
			ended = true;
			callback();
		},
	});
	const origWrite = socket.write.bind(socket);
	socket.write = (...args) => {
		writeCalls++;
		return origWrite(...args);
	};
	const origEnd = socket.end.bind(socket);
	socket.end = (...args) => {
		endCalls++;
		return origEnd(...args);
	};
	return {
		socket,
		get writeCalls() { return writeCalls; },
		get endCalls() { return endCalls; },
		writes,
		pending,
		get ended() { return ended; },
		completeNext(error = null) { pending.shift()(error); },
	};
}

// 1. Link frame (send): one socket.write holding the full [4-byte length][kind=0][payload] before the process exits.
{
	const fake = asyncSocket();
	const port = new FramedPort(fake.socket);
	const payload = new Uint8Array([10, 20, 30, 40]);
	let cbCount = 0;
	let cbErr = "unset";
	const ok = port.send(payload, (err) => { cbCount++; cbErr = err; });
	assert.equal(ok, true, "send return value under highWaterMark");
	assert.equal(fake.writeCalls, 1, "send must make one socket.write call, got " + fake.writeCalls);
	// Simulate immediate process.exit(): only writes already handed to _write reached the peer.
	assert.equal(fake.writes.length, 1, "link frame must hand one write to _write, got " + fake.writes.length);
	const frame = fake.writes[0];
	assert.equal(frame.length, 5 + payload.length, "single write must carry header and payload");
	assert.equal(frame.readUInt32BE(0), 1 + payload.length);
	assert.equal(frame[4], 0);
	assert.deepEqual(new Uint8Array(frame.subarray(5)), payload);
	assert.equal(cbCount, 0, "callback must not run before write completes");
	// disconnect() while the write is in flight must not end the socket until the write completes.
	port.disconnect();
	assert.equal(fake.ended, false, "disconnect must wait for in-flight write");
	fake.completeNext();
	assert.equal(fake.ended, true, "disconnect ends socket once write completes");
	assert.equal(cbCount, 1);
	assert.equal(cbErr, null);
}

// 2. Control frame (control): one socket.write holding the full [4-byte length][kind=1][JSON], back-pressure preserved.
{
	const fake = asyncSocket(8);
	const port = new FramedPort(fake.socket);
	const message = { event: "fatal", error: { name: "Error", message: "boom" } };
	const expectedPayload = Buffer.from(JSON.stringify(message));
	let cbCount = 0;
	let cbErr = "unset";
	const ok = port.control(message, (err) => { cbCount++; cbErr = err; });
	assert.equal(ok, false, "control must return false when frame exceeds highWaterMark");
	assert.equal(fake.writeCalls, 1, "control must make one socket.write call, got " + fake.writeCalls);
	assert.equal(fake.writes.length, 1, "control frame must hand one write to _write, got " + fake.writes.length);
	const frame = fake.writes[0];
	assert.equal(frame.length, 5 + expectedPayload.length);
	assert.equal(frame.readUInt32BE(0), 1 + expectedPayload.length);
	assert.equal(frame[4], 1);
	assert.deepEqual(frame.subarray(5), expectedPayload);
	fake.completeNext();
	assert.equal(cbCount, 1);
	assert.equal(cbErr, null);
}

// 3. Error propagation through the write callback and deferred end() on disconnect.
{
	const fake = asyncSocket();
	const port = new FramedPort(fake.socket);
	fake.socket.on("error", () => {});
	let gotErr = null;
	port.send(new Uint8Array([1]), (err) => { gotErr = err; });
	port.disconnect();
	assert.equal(fake.endCalls, 0, "disconnect must not call end() while write is in flight");
	const boom = new Error("pipe broken");
	fake.completeNext(boom);
	assert.equal(gotErr, boom);
	assert.equal(fake.endCalls, 1, "disconnect calls end() once the last in-flight write settles");
}
`
	})
}
