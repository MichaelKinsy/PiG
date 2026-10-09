package codingagent

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/nodespawn"
	"github.com/MichaelKinsy/PiG/tui"
)

// Ports utils/clipboard.ts copyToClipboard and
// utils/clipboard-command.ts runClipboardCommand. Outside Linux the native
// helper's SetText runs first; the platform commands (pbcopy, clip, the Linux
// tools) are the writers upstream tries after it.

const maxOSC52EncodedLength = 100_000

// clipboardCommandOptions mirrors runClipboardCommand's options.
type clipboardCommandOptions struct {
	input    *string
	timeout  time.Duration
	maxBytes int
}

// runClipboardCommand runs a clipboard helper. ok is false when the command
// fails, times out, or exceeds maxBytes; empty output with ok true is a
// successful result.
func runClipboardCommand(name string, args []string, options clipboardCommandOptions) (output []byte, ok bool) {
	timeout := options.timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	output, err := runClipboardCommandContext(ctx, name, args, options)
	return output, err == nil
}

// runClipboardCommandContext shares the bounded subprocess lifetime for clipboard readers and writers under the caller's deadline. A reader waits for the child to exit and every holder of its stdout to close it, even after a nonzero exit. A writer receives no output pipe to retain after daemonizing and succeeds when the child exits successfully. The deadline, parent cancellation, or output overflow kills the child and closes the parent's pipe end without joining any descendant that inherited stdin or stdout. Input strings use Node's UTF-8 encoding, replacing lone UTF-16 units.
// Ports packages/coding-agent/src/utils/clipboard-command.ts:runClipboardCommand.
func runClipboardCommandContext(parent context.Context, name string, args []string, options clipboardCommandOptions) ([]byte, error) {
	maxBytes := options.maxBytes
	if maxBytes <= 0 {
		maxBytes = 50 * 1024 * 1024
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	hideClipboardWindow(cmd)
	// The parent owns its pipe end as an *os.File and closes it itself, so Wait joins only the child and never a copy that a descendant holds open.
	parentEnd, childEnd, err := clipboardPipe(options.input == nil)
	if err != nil {
		return nil, err
	}
	stdout := nodespawn.Ignore
	if options.input != nil {
		cmd.Stdin = childEnd
	} else {
		cmd.Stdout = childEnd
		stdout = nodespawn.Pipe
	}
	// Upstream spawns with stdio ["pipe", input === undefined ? "pipe" :
	// "ignore", "ignore"] and windowsHide: true.
	nodespawn.HideWindow(cmd, nodespawn.Pipe, stdout, nodespawn.Ignore)
	// Upstream's spawn finds the command with libuv's search on Windows.
	nodespawn.SetProgram(cmd)
	err = cmd.Start()
	_ = childEnd.Close()
	if err != nil {
		_ = parentEnd.Close()
		return nil, err
	}
	var output bytes.Buffer
	var streams sync.WaitGroup
	outputDone := make(chan error, 1)
	if options.input != nil {
		outputDone <- nil
		input := jsstring.ToUTF8(*options.input)
		streams.Go(func() {
			// A writer may exit before consuming all input.
			_, _ = parentEnd.Write(input)
			_ = parentEnd.Close()
		})
	} else {
		streams.Go(func() {
			_, err := io.Copy(&limitedWriter{w: &output, remaining: maxBytes, cancel: cancel}, parentEnd)
			outputDone <- err
		})
	}
	waitErr := cmd.Wait()
	var outputErr error
	// 'close' waits for stdout even when the child exits with a failure status.
	select {
	case outputErr = <-outputDone:
	case <-ctx.Done():
		outputErr = ctx.Err()
	}
	// Upstream abort destroys the streams instead of waiting for them to close.
	_ = parentEnd.Close()
	streams.Wait()
	if waitErr != nil {
		return nil, waitErr
	}
	if outputErr != nil {
		return nil, outputErr
	}
	return output.Bytes(), nil
}

type limitedWriter struct {
	w         io.Writer
	remaining int
	cancel    context.CancelFunc
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if len(p) > l.remaining {
		l.cancel()
		return 0, errors.New("clipboard output exceeds the buffer limit")
	}
	l.remaining -= len(p)
	return l.w.Write(p)
}

// clipboardCopier holds copyToClipboard's environment so tests inject every
// dependency instead of touching the host.
type clipboardCopier struct {
	platform string // runtime.GOOS
	getenv   func(string) string
	isWSL    func() bool
	run      func(name string, args []string, options clipboardCommandOptions) ([]byte, bool)
	stdout   io.Writer
	tempDir  func() string
	native   func() *tui.NativeClipboard
}

func hostClipboardCopier() clipboardCopier {
	return clipboardCopier{
		platform: runtime.GOOS,
		getenv:   os.Getenv,
		isWSL:    isHostWSL,
		run:      runClipboardCommand,
		stdout:   os.Stdout,
		tempDir:  os.TempDir,
		native:   func() *tui.NativeClipboard { return getNativeClipboard() },
	}
}

// copyToClipboard writes text to the system clipboard. Ports upstream
// copyToClipboard.
func copyToClipboard(text string) error {
	return hostClipboardCopier().copy(text)
}

// CopyToClipboard writes text to the system clipboard, for components outside this package such as the MCP sign-in
// screen.
func CopyToClipboard(text string) error { return copyToClipboard(text) }

func (c clipboardCopier) isRemoteSession() bool {
	return c.getenv("SSH_CONNECTION") != "" || c.getenv("SSH_CLIENT") != "" || c.getenv("MOSH_CONNECTION") != ""
}

// emitOSC52 uses Buffer.from(text) semantics: lone UTF-16 units become U+FFFD before base64 encoding and the size check.
func (c clipboardCopier) emitOSC52(text string) bool {
	encoded := base64.StdEncoding.EncodeToString(jsstring.ToUTF8(text))
	if len(encoded) > maxOSC52EncodedLength {
		return false
	}
	_, _ = io.WriteString(c.stdout, "\x1b]52;c;"+encoded+"\x07")
	return true
}

// writerCommands lists the direct clipboard writers for the platform.
func (c clipboardCopier) writerCommands() [][]string {
	switch c.platform {
	case "darwin":
		return [][]string{{"pbcopy"}}
	case "windows":
		return [][]string{{"clip"}}
	}
	var commands [][]string
	if c.getenv("TERMUX_VERSION") != "" {
		commands = append(commands, []string{"termux-clipboard-set"})
	}
	if c.getenv("WAYLAND_DISPLAY") != "" {
		commands = append(commands, []string{"wl-copy"})
	}
	if c.getenv("DISPLAY") != "" {
		commands = append(commands, []string{"xclip", "-selection", "clipboard"}, []string{"xsel", "--clipboard", "--input"})
	}
	return commands
}

// copyViaWindowsClipboard writes the Windows clipboard from WSL without WSLg.
// PowerShell reads the text from a file because clip.exe and PowerShell stdin
// decode piped bytes with the console code page, which mangles UTF-8. The file uses Node's UTF-8 encoding, replacing lone UTF-16 units.
func (c clipboardCopier) copyViaWindowsClipboard(text string) bool {
	tmpFile := filepath.Join(c.tempDir(), "pi-wsl-clip-"+uuid.NewString()+".txt")
	if err := os.WriteFile(tmpFile, jsstring.ToUTF8(text), 0o600); err != nil {
		return false
	}
	defer func() { _ = os.Remove(tmpFile) }()
	output, ok := c.run("wslpath", []string{"-w", tmpFile}, clipboardCommandOptions{timeout: time.Second})
	winPath := strings.TrimSpace(string(output))
	if !ok || winPath == "" {
		return false
	}
	script := "Set-Clipboard -Value ([System.IO.File]::ReadAllText('" + strings.ReplaceAll(winPath, "'", "''") + "', [System.Text.Encoding]::UTF8))"
	_, ok = c.run("powershell.exe", []string{"-NoProfile", "-Command", script}, clipboardCommandOptions{timeout: 5 * time.Second})
	return ok
}

func (c clipboardCopier) copy(text string) error {
	copied := false
	// Direct writes precede OSC 52 so the terminal cannot race the native writer. Linux tools retain clipboard selection ownership after this call returns.
	// upstream: packages/coding-agent/src/utils/clipboard.ts:copyToClipboard
	if c.platform != "linux" && c.native != nil {
		if clipboard := c.native(); clipboard != nil && clipboard.SetText != nil {
			copied = clipboard.SetText(context.Background(), text) == nil
		}
	}
	if !copied {
		for _, command := range c.writerCommands() {
			if _, ok := c.run(command[0], command[1:], clipboardCommandOptions{input: &text, timeout: 5 * time.Second}); ok {
				copied = true
				break
			}
		}
	}
	osc52Emitted := false
	if !copied && c.platform == "linux" && c.isWSL() {
		// Windows Terminal supports OSC 52; prefer it over the slower PowerShell round trip.
		if c.getenv("WT_SESSION") != "" {
			osc52Emitted = c.emitOSC52(text)
		}
		copied = osc52Emitted || c.copyViaWindowsClipboard(text)
	}
	// OSC 52 cannot be verified, so a desktop session with a display reports
	// the failure instead. Without a display the terminal is the only route,
	// and remote sessions always emit it to reach the client clipboard.
	headless := c.platform == "linux" && c.getenv("DISPLAY") == "" && c.getenv("WAYLAND_DISPLAY") == "" && c.getenv("TERMUX_VERSION") == ""
	oversized := false
	if !osc52Emitted && (c.isRemoteSession() || (!copied && headless)) {
		if c.emitOSC52(text) {
			copied = true
		} else {
			oversized = true
		}
	}
	if copied {
		return nil
	}
	return c.unavailableError(oversized)
}

func (c clipboardCopier) unavailableError(oversized bool) error {
	switch {
	case oversized:
		return errors.New("Clipboard unavailable: text exceeds the OSC 52 size limit")
	case c.getenv("TERMUX_VERSION") != "":
		return errors.New("Clipboard unavailable: install the Termux:API app and `termux-api` package")
	case c.platform != "linux":
	case c.getenv("WAYLAND_DISPLAY") != "":
		return errors.New("Clipboard unavailable: install `wl-clipboard` (`wl-copy`) or check Wayland access")
	case c.getenv("DISPLAY") != "":
		return errors.New("Clipboard unavailable: install `xclip` or `xsel`, or check X11 access")
	}
	return errors.New("Clipboard unavailable")
}
