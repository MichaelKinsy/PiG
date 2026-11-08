package tui

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// Ports packages/chord/src/node/tui/actions.ts. The launcher's actions (slice 11 §4; fix rounds 1 to 4: Fable's H1, H2, M1, L4,
// L6 and Opus's M1, M3, L2, L3, E5, E6, E9, E12, R1): what the sandbox may ask of the machine, each confirmed by the user on
// the launcher's own prompt before it touches it. Every line the prompt shows is wrapped to the terminal's width, never cut.
// Copying writes the text to the terminal's clipboard (OSC 52, which the output filter refuses to sandboxed code) and shows it
// first (up to five rows, then how many more characters); text with control characters (a newline and a tab aside) or
// bidirectional controls is refused. Opening a URL (http or https, no user info) names its host first and runs the platform's
// opener without a shell (as the coding agent's `open-browser.ts`). Attaching refuses a path with control, format or unusual
// whitespace characters (and on Windows UNC and device paths), shows the path resolved by its text (its name on its own line)
// without looking at the machine, after the user's yes resolves its links and asks again when they lead elsewhere, and reads
// the very file confirmed (its device and inode), a regular file of at most 16 MiB, exactly at the size its open handle has.
//
// Forced Go differences. A prompt's `ask` blocks and returns the answer (a Promise); lengths Pi counts are UTF-16 units, so
// the prompt's numbers are counted as units too. Paths are resolved with the running system's own rules (Pi picks
// `posix` or `win32` by `process.platform`, its tests naming a platform for the pure checks only); `Attach`'s platform names
// the rules of its path checks.

// PromptTerminal is the launcher's prompt on the real terminal, Columns wide: Ask shows lines (the question last) and returns
// the user's answer, yes or no; Write writes to the terminal past the output filter (the launcher's own sequences).
type PromptTerminal interface {
	Columns() int
	Ask(lines []string) (bool, error)
	Write(data string)
}

// PromptLines is the prompt's rows for lines at columns wide: control characters removed, each line wrapped, never cut.
func PromptLines(lines []string, columns int) []string {
	var rows []string
	for _, line := range lines {
		rows = append(rows, Wrap(line, promptWidth(columns))...)
	}
	return rows
}

// promptWidth is the text width of a prompt row (one column of padding each side).
func promptWidth(columns int) int { return max(10, columns-2) }

// Wrap is line with control characters removed, in rows of at most width columns, counting every character but printable ASCII
// as two (wide characters never push text past the edge, where the next row would cover it).
func Wrap(line string, width int) []string {
	var rows []string
	var row strings.Builder
	used := 0
	for _, character := range visible(line) {
		cells := 2
		if character >= ' ' && character <= '~' {
			cells = 1
		}
		if used+cells > width {
			rows = append(rows, row.String())
			row.Reset()
			used = 0
		}
		row.WriteRune(character)
		used += cells
	}
	return append(rows, row.String())
}

// shownRows is how many rows of a copied text the prompt shows before "and N more characters".
const shownRows = 5

// utf16Length is text's length in UTF-16 units (JavaScript's `length`).
func utf16Length(text string) int { return len(utf16.Encode([]rune(text))) }

// controlText is whether text holds a control character (a newline and a tab aside) or a bidirectional control.
func controlText(text string) bool {
	for _, character := range text {
		switch {
		case character <= 0x08, character >= 0x0b && character <= 0x1f, character >= 0x7f && character <= 0x9f,
			character >= 0x202a && character <= 0x202e, character >= 0x2066 && character <= 0x2069:
			return true
		}
	}
	return false
}

// Opener opens a URL the user confirmed.
type Opener func(url string) error

// Act does the sandbox's request (`copy`, `open`) once the user confirmed it: whether it was done. It fails (the request
// refused) for text it will not copy and URLs it will not open. opener opens a URL (the platform's when nil).
func Act(request BridgeRequest, terminal PromptTerminal, opener Opener) (bool, error) {
	if request.Kind == "copy" {
		text := request.Text
		// Control characters (but newlines and tabs) and bidirectional controls could hide what a paste runs.
		if controlText(text) {
			return false, errors.New("copy: the text holds control characters")
		}
		lines := strings.Split(text, "\n")
		// Wrapped, never cut: a long line's end is where a pasted command would hide (Opus E6).
		var rows []string
		for _, line := range lines {
			rows = append(rows, Wrap(line, promptWidth(terminal.Columns())-2)...)
		}
		hidden := 0
		if len(rows) > shownRows {
			for _, row := range rows[shownRows:] {
				hidden += utf16Length(row)
			}
		}
		plural := "s"
		if len(lines) == 1 {
			plural = ""
		}
		question := fmt.Sprintf("Copy these %d characters (%d line%s)? [y/N]", utf16Length(text), len(lines), plural)
		var shown []string
		for _, row := range rows[:min(len(rows), shownRows)] {
			shown = append(shown, "  "+row)
		}
		if hidden > 0 {
			shown = append(shown, fmt.Sprintf("  … and %d more characters", hidden))
		}
		yes, err := terminal.Ask(append(shown, question))
		if err != nil || !yes {
			return false, err
		}
		terminal.Write("\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07")
		return true, nil
	}
	target := openable(request.URL)
	if target == nil {
		return false, errors.New("open: only http and https URLs without user info open")
	}
	yes, err := terminal.Ask([]string{"  " + target.Href(), "Open " + hostOf(target) + " in the browser? [y/N]"})
	if err != nil || !yes {
		return false, err
	}
	if opener == nil {
		opener = platformOpener
	}
	return true, opener(target.Href())
}

// MaxAttachment is the largest file the sandbox may be given: 16 MiB.
const MaxAttachment = 16 * 1024 * 1024

// AttachedFile is a file the user confirmed: its name and bytes.
type AttachedFile struct {
	Name  string
	Bytes []byte
}

// errCannotAttach is the one refusal for a file that cannot be attached.
var errCannotAttach = errors.New("attach: that file cannot be attached")

// Attach reads the file at filePath (as the sandbox gave it; `~/` the home directory) for the sandbox once the user confirmed
// it: its name and bytes, or nil when declined. Refused at once: a path with control, format or unusual whitespace characters,
// and on Windows (platform "win32") a UNC, device or `//host` path or a reserved device name. Nothing on the machine is looked
// at before the user's yes (Opus E9: on Windows a lookup of `\\host\share` alone sends the user's credentials): the prompt shows
// the path resolved by its text (`~`, `.` and `..`); after the yes its links are resolved, and a path that leads elsewhere is
// asked about again, showing where; what is opened must be the file confirmed (its device and inode as when the last prompt was
// shown). Then it is opened without blocking (a FIFO waits for no writer: Opus E12) and read only when it is a regular file of
// at most MaxAttachment bytes; one refusal for anything else (no hint what is there).
func Attach(filePath string, terminal PromptTerminal, platform string) (*AttachedFile, error) {
	if !attachable(filePath, platform) {
		return nil, errCannotAttach
	}
	home := filePath
	if filePath == "~" || strings.HasPrefix(filePath, "~/") || strings.HasPrefix(filePath, `~\`) {
		directory, err := os.UserHomeDir()
		if err != nil {
			return nil, errCannotAttach
		}
		home = cleanJoin(directory, filePath[1:])
	}
	named := resolveText(home)
	yes, err := terminal.Ask(fileLines(named, "Attach this file? [y/N]"))
	if err != nil || !yes {
		return nil, err
	}
	file, err := filepath.EvalSymlinks(named)
	if err != nil {
		return nil, errCannotAttach
	}
	// The file the user confirms, by identity: what is read must be it (Opus R1: a parent directory swapped for a link after the
	// yes would lead elsewhere, past `O_NOFOLLOW`, which guards the last step only).
	shown, err := os.Stat(file)
	if err != nil {
		return nil, errCannotAttach
	}
	if file != named {
		yes, err := terminal.Ask(fileLines(file, "It links here. Attach this file? [y/N]"))
		if err != nil || !yes {
			return nil, err
		}
	}
	// No waiting on a FIFO or device, and no link swapped in at the last step since the user saw the path.
	handle, err := openNoFollow(file)
	if err != nil {
		return nil, errCannotAttach
	}
	defer func() { _ = handle.Close() }()
	info, err := handle.Stat()
	if err != nil || !os.SameFile(info, shown) || !info.Mode().IsRegular() || info.Size() > MaxAttachment {
		return nil, errCannotAttach
	}
	// Exactly its size now: a file growing meanwhile is read up to what was checked.
	bytes := make([]byte, info.Size())
	read, err := io.ReadFull(handle, bytes)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, errCannotAttach
	}
	return &AttachedFile{Name: baseName(file), Bytes: bytes[:read]}, nil
}

// cleanJoin is directory and rest joined and cleaned, by the running system's rules.
func cleanJoin(directory, rest string) string { return filepath.Join(directory, rest) }

// resolveText is filePath made absolute and cleaned by its text alone (no link is looked at: `x/..` is dropped even where `x` is
// a file).
func resolveText(filePath string) string {
	if filepath.IsAbs(filePath) {
		return filepath.Clean(filePath)
	}
	working, _ := os.Getwd()
	return filepath.Join(working, filePath)
}

func baseName(file string) string { return filepath.Base(file) }

// attachable is whether filePath may be shown and, once confirmed, opened on platform: no character that hides or moves what
// the prompt shows (controls, format characters such as bidirectional and zero-width ones, whitespace but single spaces between
// words: Opus E5), and on Windows no UNC, device or `//host` path and no reserved device name (Opus E9).
func attachable(filePath, platform string) bool {
	previousSpace := false
	for index, character := range filePath {
		if unicode.In(character, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp) || (isJSSpace(character) && character != ' ') {
			return false
		}
		if character == ' ' {
			if previousSpace || index == 0 {
				return false
			}
		}
		previousSpace = character == ' '
	}
	if strings.HasSuffix(filePath, " ") {
		return false
	}
	if platform != "win32" {
		return true
	}
	if len(filePath) >= 2 && (filePath[0] == '\\' || filePath[0] == '/') && (filePath[1] == '\\' || filePath[1] == '/') {
		return false
	}
	for _, segment := range strings.FieldsFunc(filePath, func(r rune) bool { return r == '\\' || r == '/' }) {
		if reservedDevice(segment) {
			return false
		}
	}
	return true
}

// isJSSpace is JavaScript's `\s`.
func isJSSpace(character rune) bool {
	switch {
	case character >= 0x09 && character <= 0x0d, character == ' ', character == 0xa0, character == 0x1680,
		character >= 0x2000 && character <= 0x200a, character == 0x2028, character == 0x2029, character == 0x202f,
		character == 0x205f, character == 0x3000, character == 0xfeff:
		return true
	}
	return false
}

// reservedDevice is whether segment is a Windows reserved device name: `^(?:con|prn|aux|nul|com\d|lpt\d|conin$|conout$)(?:\..*)?$`
// (any case).
func reservedDevice(segment string) bool {
	name := strings.ToLower(segment)
	if dot := strings.IndexByte(name, '.'); dot >= 0 {
		name = name[:dot]
	}
	switch name {
	case "con", "prn", "aux", "nul", "conin$", "conout$":
		return true
	}
	if len(name) == 4 && (strings.HasPrefix(name, "com") || strings.HasPrefix(name, "lpt")) && name[3] >= '0' && name[3] <= '9' {
		return true
	}
	return false
}

// fileLines is the prompt's lines for the file at file: its name, its directory, then question.
func fileLines(file, question string) []string {
	return []string{"File: " + baseName(file), "In:   " + filepath.Dir(file), question}
}

// visible is text with control and format characters (bidirectional, zero width) removed: sandboxed text in the prompt.
func visible(text string) string {
	return strings.Map(func(character rune) rune {
		if unicode.In(character, unicode.Cc, unicode.Cf) {
			return -1
		}
		return character
	}, text)
}

// openable is the URL text names when it is an http or https URL without user info (WHATWG parsing, as `new URL`); else nil.
func openable(text string) *nodeurl.URL {
	value, err := nodeurl.ParseURL(text)
	if err != nil || (value.Protocol != "http:" && value.Protocol != "https:") || value.Username != "" || value.Password != "" {
		return nil
	}
	return &value
}

// hostOf is a URL's `host`: its hostname and its port when it is not the scheme's default.
func hostOf(value *nodeurl.URL) string {
	if value.Port == "" {
		return value.Hostname
	}
	return value.Hostname + ":" + value.Port
}

// OpenerCommand is the command and arguments opening target on goos, as the coding agent opens a browser (`open-browser.ts`):
// never a shell (on Windows not `cmd /c start`, whose parsing runs what follows a `&`): `rundll32 url.dll,FileProtocolHandler
// <url>` on Windows, `open` on macOS, `xdg-open` elsewhere, the URL one argument.
func OpenerCommand(target, goos string) (string, []string) {
	switch goos {
	case "windows", "win32":
		return "rundll32", []string{"url.dll,FileProtocolHandler", target}
	case "darwin":
		return "open", []string{target}
	}
	return "xdg-open", []string{target}
}

// platformOpener opens target with the platform's opener (OpenerCommand), detached, best effort: a missing opener is no crash.
func platformOpener(target string) error {
	command, args := OpenerCommand(target, runtime.GOOS)
	// The root as its directory: no opener needs the launcher's (a WSL path too long for explorer.exe, pi #11592).
	cmd := exec.Command(command, args...)
	cmd.Dir = string(os.PathSeparator)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return nil
	}
	//portlint:allow golifetime reaps the opener when it exits; it is detached and the launcher never waits for it
	go func() { _ = cmd.Wait() }()
	return nil
}

// runtimePlatform is the platform name Attach's path checks take for this system.
func runtimePlatform() string {
	if runtime.GOOS == "windows" {
		return "win32"
	}
	return runtime.GOOS
}
