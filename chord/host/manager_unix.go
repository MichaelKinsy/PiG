//go:build unix

package host

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

func lockAddress(key string) (string, bool, error) {
	if runtime.GOOS == "linux" || runtime.GOOS == "android" {
		// Node's `\0chord2-<key>`; Go writes the abstract namespace's leading NUL as @.
		return "@chord2-" + key, false, nil
	}
	address, err := lockSocketPath("chord2-" + key + ".sock")
	return address, true, err
}

// maxSocketPathBytes: sun_path holds 104 bytes on macOS and the BSDs, including the terminating NUL.
const maxSocketPathBytes = 103

// lockSocketPath is manager.ts lockSocketPath: the lock socket's path in the temporary directory, or, when that path
// does not fit in sun_path (a deep TMPDIR, such as a test runner's below macOS's per-user one), in a private per-user
// directory under /tmp. Other users must not be able to create or remove sockets there, so the directory has to be
// ours and closed to everyone else.
func lockSocketPath(file string) (string, error) {
	preferred := filepath.Join(os.TempDir(), file)
	if len(preferred) <= maxSocketPathBytes {
		return preferred, nil
	}
	uid := os.Getuid()
	directory := "/tmp/chord2-" + strconv.Itoa(uid)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || int(stat.Uid) != uid || info.Mode().Perm()&0o077 != 0 {
		return "", NewChordError(CodeUnavailable, "lock directory "+directory+" must be a directory only its owner can access", ChordErrorOptions{})
	}
	return filepath.Join(directory, file), nil
}

func listenLock(address string) (net.Listener, error) { return net.Listen("unix", address) }

func lockInUse(err error) bool { return errors.Is(err, syscall.EADDRINUSE) }

// accepting is manager.ts accepting(address): a refused or missing socket is not held.
func accepting(address string) bool {
	conn, err := net.Dial("unix", address)
	if err == nil {
		_ = conn.Close()
		return true
	}
	return !errors.Is(err, syscall.ECONNREFUSED) && !errors.Is(err, syscall.ENOENT)
}

// ProcessChannel is Pi's `process` as an IPC channel: the Node IPC channel the parent opened on NODE_CHANNEL_FD
// (manager.ts childControl's default channel). Without one, as in a process nobody forked, the channel is not
// connected and sends are skipped.
func ProcessChannel() IpcChannel {
	processChannelOnce.Do(func() {
		processChannel = &nodeChannel{listeners: map[string][]*IpcListener{}}
		fd, err := strconv.Atoi(os.Getenv("NODE_CHANNEL_FD"))
		if err != nil || os.Getenv("NODE_CHANNEL_SERIALIZATION_MODE") != "json" {
			return
		}
		conn, err := net.FileConn(os.NewFile(uintptr(fd), "ipc"))
		if err != nil {
			return
		}
		processChannel = newNodeChannel(conn)
	})
	return processChannel
}

// ForkDriver is manager.ts forkDriver: it forks options.Entry under Node (node from PATH, Pi's process.execPath) with
// Node's IPC channel, as child_process.fork does. The child is detached: it leads a process group, so the whole tree
// can be killed.
func ForkDriver(options ForkDriverOptions) Driver { return forkDriver{options: options} }

type forkDriver struct{ options ForkDriverOptions }

func (driver forkDriver) Spawn() (Instance, error) {
	options := driver.options
	node, err := exec.LookPath("node")
	if err != nil {
		return nil, err
	}
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, err
	}
	syscall.CloseOnExec(fds[0])
	parentEnd, childEnd := os.NewFile(uintptr(fds[0]), "ipc"), os.NewFile(uintptr(fds[1]), "ipc")
	defer func() { _ = childEnd.Close() }()
	cmd := exec.Command(node, forkArguments(options)...)
	cmd.Env = forkEnvironment(options, "3")
	cmd.ExtraFiles = []*os.File{childEnd}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	taps, err := openOutputTaps(options)
	if err != nil {
		_ = parentEnd.Close()
		return nil, err
	}
	if taps != nil {
		cmd.Stdout, cmd.Stderr = taps[0].write, taps[1].write
	} else {
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	}
	if err := cmd.Start(); err != nil {
		_ = parentEnd.Close()
		closeOutputTaps(taps)
		return nil, err
	}
	pumpOutputTaps(taps, options)
	conn, err := net.FileConn(parentEnd)
	_ = parentEnd.Close()
	if err != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return nil, err
	}
	pid := cmd.Process.Pid
	instance := newForkInstance(options, pid, newNodeChannel(conn), func() {
		if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
			_ = cmd.Process.Kill()
		}
	})
	//portlint:allow golifetime the waiter returns when the leader exits
	go func() {
		state, _ := cmd.Process.Wait()
		// However the leader ended (graceful, crash, kill), nothing it spawned outlives it.
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		instance.end(exitOf(state))
	}()
	return instance, nil
}

func exitOf(state *os.ProcessState) Exit {
	if state == nil {
		return Exit{}
	}
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return Exit{Signal: new(unix.SignalName(status.Signal()))}
	}
	return Exit{Code: new(state.ExitCode())}
}
