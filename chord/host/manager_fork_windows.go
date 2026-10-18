//go:build windows

package host

// Ports packages/chord/src/manager.ts: the Windows half of forkDriver and the process IPC channel (Node IPC framing over a named pipe).

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ProcessChannel is Pi's `process` as an IPC channel: the Node IPC channel the parent opened, which Node passes as a
// CRT file descriptor (NODE_CHANNEL_FD) on a named pipe. The descriptor's OS handle (msvcrt's _get_osfhandle) is
// read and written with libuv's IPC framing. Without one, as in a process nobody forked, the channel is not connected
// and sends are skipped.
func ProcessChannel() IpcChannel {
	processChannelOnce.Do(func() {
		processChannel = &nodeChannel{listeners: map[string][]*IpcListener{}}
		fd, err := strconv.Atoi(os.Getenv("NODE_CHANNEL_FD"))
		if err != nil || os.Getenv("NODE_CHANNEL_SERIALIZATION_MODE") != "json" {
			return
		}
		pipe, err := adoptPipe(fd)
		if err != nil {
			return
		}
		processChannel = newNodeChannel(newIPCFrames(pipe))
	})
	return processChannel
}

// ForkDriver is manager.ts forkDriver: it forks options.Entry under Node (node from PATH, Pi's process.execPath) with
// Node's IPC channel, as child_process.fork does. Node's own spawn on Windows hands the child its stdio as CRT file
// descriptors, the IPC pipe on descriptor 3, through STARTUPINFO's lpReserved2 (libuv src/win/process-stdio.c), which
// Go's os/exec cannot, so the process is created here. The child is detached (DETACHED_PROCESS and a process group of
// its own, as libuv does for detached) and placed in a job object, so the whole tree can be killed: Windows has no
// process groups to signal.
func ForkDriver(options ForkDriverOptions) Driver { return forkDriver{options: options} }

type forkDriver struct{ options ForkDriverOptions }

// The IPC channel is descriptor 3, as child_process.fork's stdio ["ignore", ..., ..., "ipc"].
const channelDescriptor = 3

// stdioSlot is one descriptor of the child's table.
type stdioSlot struct {
	handle windows.Handle
	flags  byte
}

// inheritable is an inheritable duplicate of handle (libuv uv__duplicate_handle).
func inheritable(handle windows.Handle) (windows.Handle, error) {
	process := windows.CurrentProcess()
	var duplicate windows.Handle
	if err := windows.DuplicateHandle(process, handle, process, &duplicate, 0, true, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return 0, err
	}
	return duplicate, nil
}

// nulHandle is an inheritable handle on NUL, the ignored stdin (libuv uv__create_nul_handle).
func nulHandle(access uint32) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString("NUL")
	if err != nil {
		return 0, err
	}
	attributes := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	return windows.CreateFile(name, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, &attributes, windows.OPEN_EXISTING, 0, 0)
}

// outputSlot is the child's stdout or stderr: write when the output is tapped, else the parent's own stream. A stream
// that is not open is NUL, as libuv gives an ignored one.
func outputSlot(stream *os.File) (stdioSlot, error) {
	handle := windows.Handle(stream.Fd())
	if handle != 0 && handle != windows.InvalidHandle {
		if kind, err := windows.GetFileType(handle); err == nil {
			if duplicate, err := inheritable(handle); err == nil {
				switch kind {
				case windows.FILE_TYPE_DISK:
					return stdioSlot{duplicate, crtOpen}, nil
				case windows.FILE_TYPE_PIPE:
					return stdioSlot{duplicate, crtOpen | crtPipe}, nil
				default:
					return stdioSlot{duplicate, crtOpen | crtDevice}, nil
				}
			}
		}
	}
	nul, err := nulHandle(windows.FILE_GENERIC_WRITE | windows.FILE_READ_ATTRIBUTES)
	return stdioSlot{nul, crtOpen | crtDevice}, err
}

// newIPCPipe creates the connected pipe pair of an IPC channel: the connection the parent keeps (overlapped) and the
// handle the child gets, inheritable (libuv uv__create_stdio_pipe_pair with an ipc parent pipe). The pipe has a random
// name and only this user can open it. The parent's end is the one server instance, created listening before the
// child's end opens it, so that open can never find the name without a free instance (ERROR_PIPE_BUSY), which a
// listener that makes its connectable instance in a goroutine of its own allowed.
func newIPCPipe() (io.ReadWriteCloser, windows.Handle, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, 0, err
	}
	name := `\\.\pipe\chord2-ipc-` + strconv.Itoa(os.Getpid()) + "-" + hex.EncodeToString(random[:])
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, 0, err
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return nil, 0, err
	}
	pointer, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, 0, err
	}
	server, err := windows.CreateNamedPipe(pointer,
		windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_FIRST_PIPE_INSTANCE|windows.FILE_FLAG_OVERLAPPED,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 65536, 65536, 0,
		&windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor})
	if err != nil {
		return nil, 0, err
	}
	attributes := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	child, err := windows.CreateFile(pointer, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, &attributes, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		_ = windows.CloseHandle(server)
		return nil, 0, err
	}
	return newHandlePipe(server, false), child, nil
}

// startupInfoEx is STARTUPINFOEXW, whose lpReserved2 the windows package's StartupInfo does not let us set.
type startupInfoEx struct {
	size          uint32
	_             *uint16
	desktop       *uint16
	title         *uint16
	x, y          uint32
	xSize, ySize  uint32
	xChars        uint32
	yChars        uint32
	fillAttribute uint32
	flags         uint32
	showWindow    uint16
	reservedSize  uint16
	reserved      *byte
	stdInput      windows.Handle
	stdOutput     windows.Handle
	stdError      windows.Handle
	attributes    *windows.ProcThreadAttributeList
}

func (driver forkDriver) Spawn() (Instance, error) {
	options := driver.options
	node, err := exec.LookPath("node")
	if err != nil {
		return nil, err
	}
	taps, err := openOutputTaps(options)
	if err != nil {
		return nil, err
	}
	var owned []windows.Handle
	release := func() {
		for _, handle := range owned {
			_ = windows.CloseHandle(handle)
		}
		owned = nil
	}
	fail := func(err error) (Instance, error) {
		release()
		closeOutputTaps(taps)
		return nil, err
	}
	slots := make([]stdioSlot, channelDescriptor+1)
	stdin, err := nulHandle(windows.FILE_GENERIC_READ)
	if err != nil {
		return fail(err)
	}
	owned = append(owned, stdin)
	slots[0] = stdioSlot{stdin, crtOpen | crtDevice}
	for i, mirror := range []*os.File{os.Stdout, os.Stderr} {
		stream := mirror
		if taps != nil {
			stream = taps[i].write
		}
		slots[i+1], err = outputSlot(stream)
		if slots[i+1].handle != 0 {
			owned = append(owned, slots[i+1].handle)
		}
		if err != nil {
			return fail(err)
		}
	}
	conn, child, err := newIPCPipe()
	if err != nil {
		return fail(err)
	}
	owned = append(owned, child)
	slots[channelDescriptor] = stdioSlot{child, crtOpen | crtPipe}

	line, err := windows.UTF16FromString(windows.ComposeCommandLine(append([]string{node}, forkArguments(options)...)))
	if err != nil {
		_ = conn.Close()
		return fail(err)
	}
	application, err := windows.UTF16PtrFromString(node)
	if err != nil {
		_ = conn.Close()
		return fail(err)
	}
	environment, err := environmentBlock(forkEnvironment(options, strconv.Itoa(channelDescriptor)))
	if err != nil {
		_ = conn.Close()
		return fail(err)
	}
	// Only the stdio handles are inherited, whatever else this process has marked inheritable.
	handles := make([]windows.Handle, len(slots))
	for i, slot := range slots {
		handles[i] = slot.handle
	}
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		_ = conn.Close()
		return fail(err)
	}
	defer attributes.Delete()
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])); err != nil { //nolint:gosec // G103: the handle list is the attribute value PROC_THREAD_ATTRIBUTE_HANDLE_LIST takes, as x/sys documents.
		_ = conn.Close()
		return fail(err)
	}
	flags := make([]byte, len(slots))
	for i, slot := range slots {
		flags[i] = slot.flags
	}
	raw := make([]uintptr, len(handles))
	for i, handle := range handles {
		raw[i] = uintptr(handle)
	}
	table := childStdio(flags, raw)
	info := startupInfoEx{
		flags:        windows.STARTF_USESTDHANDLES,
		reservedSize: uint16(len(table)),
		reserved:     &table[0],
		stdInput:     slots[0].handle,
		stdOutput:    slots[1].handle,
		stdError:     slots[2].handle,
		attributes:   attributes.List(),
	}
	info.size = uint32(unsafe.Sizeof(info))
	var process windows.ProcessInformation
	const creation = windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_SUSPENDED |
		windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP
	r, _, errno := procCreateProcessW.Call(
		uintptr(unsafe.Pointer(application)), uintptr(unsafe.Pointer(&line[0])), 0, 0, 1, creation, //nolint:gosec // G103: CreateProcessW reads the strings and STARTUPINFOEXW layouts built here and fills the process information; x/sys cannot set lpReserved2.
		uintptr(unsafe.Pointer(&environment[0])), 0, uintptr(unsafe.Pointer(&info)), uintptr(unsafe.Pointer(&process))) //nolint:gosec // G103: see above.
	if r == 0 {
		_ = conn.Close()
		return fail(os.NewSyscallError("CreateProcess", errno))
	}
	release()
	pumpOutputTaps(taps, options)

	job, err := windows.CreateJobObject(nil, nil)
	if err == nil {
		if err = windows.AssignProcessToJobObject(job, process.Process); err != nil {
			// Not in the job (a job this process runs in forbids it): the child alone can then be killed.
			_ = windows.CloseHandle(job)
			job = 0
		}
	} else {
		job = 0
	}
	if _, err := windows.ResumeThread(process.Thread); err != nil {
		_ = windows.TerminateProcess(process.Process, 1)
		if job != 0 {
			_ = windows.CloseHandle(job)
		}
		_ = windows.CloseHandle(process.Thread)
		_ = windows.CloseHandle(process.Process)
		_ = conn.Close()
		return nil, err
	}
	_ = windows.CloseHandle(process.Thread)

	var (
		mu     sync.Mutex
		ended  bool
		killed atomic.Bool
	)
	// terminate ends the leader and everything in its job, once; afterwards the handles are closed and it does nothing.
	terminate := func() {
		mu.Lock()
		defer mu.Unlock()
		if ended {
			return
		}
		if job != 0 {
			_ = windows.TerminateJobObject(job, 1)
		} else {
			_ = windows.TerminateProcess(process.Process, 1)
		}
	}
	instance := newForkInstance(options, int(process.ProcessId), newNodeChannel(newIPCFrames(conn)), func() {
		killed.Store(true)
		terminate()
	})
	go func() {
		_, _ = windows.WaitForSingleObject(process.Process, windows.INFINITE)
		var code uint32
		_ = windows.GetExitCodeProcess(process.Process, &code)
		mu.Lock()
		// However the leader ended (graceful, crash, kill), nothing it spawned outlives it.
		if job != 0 {
			_ = windows.TerminateJobObject(job, 1)
			_ = windows.CloseHandle(job)
		}
		ended = true
		mu.Unlock()
		_ = windows.CloseHandle(process.Process)
		if killed.Load() {
			// libuv reports the signal uv_process_kill sent for a process it terminated.
			instance.end(Exit{Signal: new("SIGKILL")})
			return
		}
		instance.end(Exit{Code: new(int(code))})
	}()
	return instance, nil
}
