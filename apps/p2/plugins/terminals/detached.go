package terminals

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// Ports apps/p2/plugins/terminals/detached.ts: attachments to the detached terminal host: their lifetime is a socket, not
// ownership of the host process.

// HostCommand is the argument that makes the p2 executable the terminal host (`p2 terminals-host <data directory>`): like
// the session worker it is the same binary (D473), so it is what Pi's `main.ts` run by path is.
const HostCommand = "terminals-host"

// HostAddress is where the data directory's terminal host listens: a Linux abstract socket ('@' is Go's spelling of a
// leading NUL), a file in the temporary directory elsewhere, a named pipe on Windows.
func HostAddress(directory string) (string, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", err
	}
	if real, err = filepath.Abs(real); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(real + ":p2-terminals"))
	key := hex.EncodeToString(sum[:])[:32]
	switch runtime.GOOS {
	case "windows":
		return `\\.\pipe\p2-terminals-` + key, nil
	case "linux", "android":
		return "@p2-terminals-" + key, nil
	}
	return filepath.Join(os.TempDir(), "p2-"+key+"-t.sock"), nil
}

type executableVersion struct {
	path    string
	size    int64
	modTime time.Time
	version string
}

var (
	versionMu    sync.Mutex
	versionCache executableVersion
)

// HostVersion is the terminal host's code version: a hash of the p2 executable it runs (Pi hashes the files of the host,
// `terminalHostVersion`; here the host is the executable, which carries ghostty-vt.wasm too). A hash of an unchanged file
// is remembered.
func HostVersion() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	versionMu.Lock()
	defer versionMu.Unlock()
	if cached := versionCache; cached.path == path && cached.size == info.Size() && cached.modTime.Equal(info.ModTime()) {
		return cached.version, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	version := hex.EncodeToString(hash.Sum(nil))[:16]
	versionCache = executableVersion{path: path, size: info.Size(), modTime: info.ModTime(), version: version}
	return version, nil
}

func connectOnce(address string) (net.Conn, error) { return net.Dial("unix", address) }

// ConnectRunningTerminalHost connects to the data directory's terminal host when one runs; nil when none does (none is
// started).
func ConnectRunningTerminalHost(directory string) net.Conn {
	address, err := HostAddress(directory)
	if err != nil {
		return nil
	}
	conn, err := connectOnce(address)
	if err != nil {
		return nil
	}
	return conn
}

// hostStartTimeout is how long a starting host may take to listen.
const hostStartTimeout = 10 * time.Second

// ConnectTerminalHost connects to the data directory's terminal host, starting it when none runs.
func ConnectTerminalHost(ctx context.Context, directory string) (net.Conn, error) {
	address, err := HostAddress(directory)
	if err != nil {
		return nil, err
	}
	if conn, err := connectOnce(address); err == nil {
		return conn, nil
	}
	if err := spawnHost(directory); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(hostStartTimeout)
	wait := time.NewTimer(0)
	defer wait.Stop()
	<-wait.C
	var last error
	for {
		conn, err := connectOnce(address)
		if err == nil {
			return conn, nil
		}
		last = err
		if time.Now().After(deadline) {
			return nil, errors.Join(errors.New("Terminal host did not start (see Terminal host in Logs)"), last)
		}
		wait.Reset(100 * time.Millisecond)
		select {
		case <-wait.C:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
}

// spawnHost starts the terminal host, detached: its own session, no stdio (the host keeps a bounded, redacted snapshot of
// its logs, including its startup failures).
func spawnHost(directory string) error {
	path, err := os.Executable()
	if err != nil {
		return err
	}
	command := exec.Command(path, HostCommand, directory)
	detach(command)
	if err := command.Start(); err != nil {
		// Pi's `child.on("error", () => {})`: the wait for the socket reports the failure.
		return nil
	}
	//portlint:allow golifetime reaps the host when it exits
	go func() { _ = command.Wait() }()
	return nil
}

// Attachment is one attachment to the detached terminal host: a socket whose end never stops the host or its programs.
type Attachment struct {
	ready     chan struct{}
	readyErr  error
	readyOnce sync.Once

	mu         sync.Mutex
	writer     *frameWriter
	ended      bool
	onDetached func()
}

var errDisconnected = errors.New("Terminal host is disconnected")

func (a *Attachment) resolve(err error) {
	a.readyOnce.Do(func() {
		a.readyErr = err
		close(a.ready)
	})
}

// Ready waits until the host said hello; it fails when the host could not be reached or the socket closed before.
func (a *Attachment) Ready(ctx context.Context) error {
	select {
	case <-a.ready:
		return a.readyErr
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// end closes the attachment; notify: tell onDetached (the socket closed on its own).
func (a *Attachment) end(notify bool) {
	a.mu.Lock()
	if a.ended {
		a.mu.Unlock()
		return
	}
	a.ended = true
	writer := a.writer
	a.writer = nil
	a.mu.Unlock()
	if writer != nil {
		writer.destroy()
	}
	a.resolve(errDisconnected)
	if notify {
		a.onDetached()
	}
}

// Write sends a message (a JSON value) or bytes to the host.
func (a *Attachment) Write(message any) error {
	frame, err := EncodeFrame(message)
	if err != nil {
		return err
	}
	a.mu.Lock()
	writer := a.writer
	a.mu.Unlock()
	if writer == nil || writer.destroyed.Load() {
		return errDisconnected
	}
	if !writer.send(frame) {
		return errors.New("Terminal input backlog")
	}
	return nil
}

// Detach closes the socket; onDetached is not called for it.
func (a *Attachment) Detach() { a.end(false) }

// AttachTerminalHost attaches to the data directory's terminal host (starting it when none runs): frames go to onFrame, and
// onDetached learns when the socket closed on its own (the host exited, a connection error). Both run on the attachment's
// reader goroutine.
func AttachTerminalHost(directory string, onFrame func(Frame), onDetached func()) *Attachment {
	a := &Attachment{ready: make(chan struct{}), onDetached: onDetached}
	//portlint:allow golifetime ends with the connection attempt or the socket
	go func() {
		//portlint:allow ctxbackground an attachment ends with its socket (Detach), not with a caller's context
		conn, err := ConnectTerminalHost(context.Background(), directory)
		if err != nil {
			a.resolve(err)
			a.end(true)
			return
		}
		writer := newFrameWriter(conn)
		a.mu.Lock()
		if a.ended {
			a.mu.Unlock()
			_ = conn.Close()
			return
		}
		a.writer = writer
		a.mu.Unlock()
		//portlint:allow golifetime the writer ends when the socket is destroyed
		go writer.run()
		_ = ReadFrames(conn, func(frame Frame) {
			if !frame.Raw {
				if event, err := ParseEvent(frame.JSON); err == nil && event.Type == EventHello {
					a.resolve(nil)
				}
			}
			onFrame(frame)
		})
		a.end(true)
	}()
	return a
}
