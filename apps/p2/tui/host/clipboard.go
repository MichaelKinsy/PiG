package host

// Launcher-only clipboard writer, copied from coding-agent utils/clipboard.ts (copyToClipboard), clipboard-command.ts and wsl.ts. OSC 52 is written by
// the launcher itself (the worker's OSC is always filtered). Only called under a grant (p2-tui-port.md section 3.1 rules 4-5).
//
// pi: apps/p2/src/hosts/tui-clipboard.ts
//

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	pitui "github.com/MichaelKinsy/PiG/tui"
	"github.com/google/uuid"
)

const maxOSC52EncodedLength = 100_000

// runClipboardCommand runs a clipboard tool; ok is false when it failed or timed out (an empty output is a successful result).
func runClipboardCommand(name string, args []string, input *string, timeout time.Duration) (output []byte, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	if input != nil {
		command.Stdin = strings.NewReader(*input)
	} else {
		command.Stdout = &stdout
	}
	// Clipboard writers can daemonize: they get no output pipe to retain, and the wait does not join descendants.
	command.WaitDelay = 100 * time.Millisecond
	if err := command.Run(); err != nil {
		return nil, false
	}
	return stdout.Bytes(), true
}

func isWSL(getenv func(string) string) bool {
	if getenv("WSL_DISTRO_NAME") != "" || getenv("WSLENV") != "" {
		return true
	}
	data, err := os.ReadFile("/proc/version")
	return err == nil && regexp.MustCompile(`(?i)microsoft|wsl`).Match(data)
}

func copyViaWindowsClipboard(text string) bool {
	file := filepath.Join(os.TempDir(), "p2-wsl-clip-"+uuid.NewString()+".txt")
	if os.WriteFile(file, []byte(text), 0o600) != nil {
		return false
	}
	defer os.Remove(file)
	path, ok := runClipboardCommand("wslpath", []string{"-w", file}, nil, time.Second)
	winPath := strings.TrimSpace(string(path))
	if !ok || winPath == "" {
		return false
	}
	script := "Set-Clipboard -Value ([System.IO.File]::ReadAllText('" + strings.ReplaceAll(winPath, "'", "''") + "', [System.Text.Encoding]::UTF8))"
	_, ok = runClipboardCommand("powershell.exe", []string{"-NoProfile", "-Command", script}, nil, 5*time.Second)
	return ok
}

type clipboardCommand struct {
	name string
	args []string
}

// CopyToClipboard writes text to the visitor's clipboard: platform tools, then OSC 52 through writeTerminal.
func CopyToClipboard(text string, writeTerminal func(data string), getenv func(string) string, platform string) error {
	if getenv == nil {
		getenv = os.Getenv
	}
	if platform == "" {
		platform = runtime.GOOS
	}
	osc52 := func() bool {
		encoded := base64.StdEncoding.EncodeToString([]byte(text))
		if len(encoded) > maxOSC52EncodedLength {
			return false
		}
		writeTerminal("\x1b]52;c;" + encoded + "\x07")
		return true
	}
	var commands []clipboardCommand
	switch platform {
	case "darwin":
		commands = append(commands, clipboardCommand{"pbcopy", nil})
	case "windows", "win32":
		commands = append(commands, clipboardCommand{"clip", nil})
	default:
		if getenv("TERMUX_VERSION") != "" {
			commands = append(commands, clipboardCommand{"termux-clipboard-set", nil})
		}
		if getenv("WAYLAND_DISPLAY") != "" {
			commands = append(commands, clipboardCommand{"wl-copy", nil})
		}
		if getenv("DISPLAY") != "" {
			commands = append(commands, clipboardCommand{"xclip", []string{"-selection", "clipboard"}}, clipboardCommand{"xsel", []string{"--clipboard", "--input"}})
		}
	}
	copied := false
	if platform != "linux" {
		if native := pitui.GetNativeClipboard(); native != nil && native.SetText != nil && native.SetText(context.Background(), text) == nil {
			copied = true
		}
	}
	for _, command := range commands {
		if copied {
			break
		}
		if _, ok := runClipboardCommand(command.name, command.args, &text, 5*time.Second); ok {
			copied = true
			break
		}
	}
	osc52Emitted := false
	if !copied && platform == "linux" && isWSL(getenv) {
		if getenv("WT_SESSION") != "" {
			osc52Emitted = osc52()
		}
		copied = osc52Emitted || copyViaWindowsClipboard(text)
	}
	// OSC 52 cannot be verified: a desktop session with a display reports the failure instead; headless and remote sessions use the terminal's
	// clipboard.
	remote := getenv("SSH_CONNECTION") != "" || getenv("SSH_CLIENT") != "" || getenv("MOSH_CONNECTION") != ""
	headless := platform == "linux" && getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" && getenv("TERMUX_VERSION") == ""
	if !osc52Emitted && (remote || (!copied && headless)) {
		if osc52() {
			copied = true
		} else if !copied {
			return errors.New("Clipboard unavailable: text exceeds the OSC 52 size limit")
		}
	}
	if !copied {
		return errors.New("Clipboard unavailable (install wl-copy, xclip or xsel)")
	}
	return nil
}

// OpenBrowser starts the platform opener with argv, never a shell.
func OpenBrowser(target, platform string) {
	if platform == "" {
		platform = runtime.GOOS
	}
	var command *exec.Cmd
	switch platform {
	case "darwin":
		command = exec.Command("open", target)
	case "windows", "win32":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		command = exec.Command("xdg-open", target)
	}
	if command.Start() == nil {
		go func() { _ = command.Wait() }()
	}
}

// ReadClipboardText reads text with the platform tools; "" when there is none.
func ReadClipboardText(getenv func(string) string, platform string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	if platform == "" {
		platform = runtime.GOOS
	}
	var commands []clipboardCommand
	if getenv("TERMUX_VERSION") != "" {
		commands = append(commands, clipboardCommand{"termux-clipboard-get", nil})
	}
	if platform == "linux" {
		if getenv("WAYLAND_DISPLAY") != "" {
			commands = append(commands, clipboardCommand{"wl-paste", []string{"--no-newline", "--type", "text"}})
		}
		if getenv("DISPLAY") != "" {
			commands = append(commands, clipboardCommand{"xclip", []string{"-selection", "clipboard", "-out"}}, clipboardCommand{"xsel", []string{"--clipboard", "--output"}})
		}
	}
	if platform == "darwin" {
		commands = append(commands, clipboardCommand{"pbpaste", nil})
	}
	for _, command := range commands {
		if out, ok := runClipboardCommand(command.name, command.args, nil, 5*time.Second); ok {
			return string(out)
		}
	}
	if native := pitui.GetNativeClipboard(); native != nil && native.GetText != nil {
		if value, available, err := native.GetText(context.Background()); err == nil && available && value != nil {
			return *value
		}
	}
	return ""
}

// ClipboardImage is an image read from the clipboard.
type ClipboardImage struct {
	Bytes    []byte
	MimeType string
}

// ReadClipboardImage reads a PNG, JPEG, WebP or GIF image from the clipboard; nil when there is none.
func ReadClipboardImage() *ClipboardImage {
	data, mime, err := codingagent.ReadClipboardImage()
	if err != nil || len(data) == 0 {
		return nil
	}
	switch mime {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return &ClipboardImage{Bytes: data, MimeType: mime}
	}
	return nil
}

// ReadClipboardFilePaths is copied files (Finder file copies); nil when there are none.
func ReadClipboardFilePaths() []string {
	native := pitui.GetNativeClipboard()
	if native == nil || native.GetFilePaths == nil {
		return nil
	}
	paths, available, err := native.GetFilePaths(context.Background())
	if err != nil || !available {
		return nil
	}
	return paths
}
