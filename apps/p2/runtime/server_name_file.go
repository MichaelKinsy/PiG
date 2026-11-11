package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
)

// Ports apps/p2/src/runtime/server-name-file.ts: the server's name on disk and the device's name it falls back to.
//
// The name belongs to the server, not to Radius: `<data>/identity.json` (`{ "name": "Studio" }`) lives beside the sessions,
// works without Radius and survives `radius:login` and signing out (which rewrite or remove `radius.json`). The file exists
// only while the owner has named the server; without it the server is called by the device's name ("Cristina's MacBook
// Pro"), read again at every start, so renaming the device renames an unnamed server. Radius keeps a copy for its shell
// (plugins/radius/contract/brain.go, "Name"); the copy never flows back.
//
// The name is a label, never an identity or a credential: the Radius server ID stays the server's identity, and nothing
// authorizes by name. Places of projects name this machine by the host name, not by this name.

func identityPath(directory string) string { return filepath.Join(directory, "identity.json") }

// NormalizeServerName is name as one trimmed line without control characters, at most MaxServerNameLength characters; ok is
// false if nothing is left.
func NormalizeServerName(name string) (string, bool) {
	var line strings.Builder
	space := false
	for _, r := range name {
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) || isJSSpace(r) {
			space = true
			continue
		}
		if space && line.Len() > 0 {
			line.WriteByte(' ')
		}
		space = false
		line.WriteRune(r)
	}
	clipped := []rune(line.String())
	if len(clipped) > MaxServerNameLength {
		clipped = clipped[:MaxServerNameLength]
	}
	// Clipping may leave a space at the end; the one before the first character was never written.
	result := strings.TrimRightFunc(string(clipped), isJSSpace)
	return result, result != ""
}

// DeviceName is the device's name, best effort: on macOS the computer name from System Settings ("Cristina’s MacBook Air"),
// else the host name the operating system reports, without the `.local` that macOS adds to it. ok is false when neither
// gives one; the server then has no name and Radius shows its server ID.
func DeviceName() (name string, ok bool) {
	if runtime.GOOS == "darwin" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if stdout, err := exec.CommandContext(ctx, "scutil", "--get", "ComputerName").Output(); err == nil {
			// No computer name set (or no scutil): the host name below stands in.
			if name, ok := NormalizeServerName(string(stdout)); ok {
				return name, true
			}
		}
	}
	host, err := os.Hostname()
	if err != nil {
		// The host name can be unavailable (restricted sandboxes): the server then has no name.
		return "", false
	}
	if len(host) >= 6 && strings.EqualFold(host[len(host)-6:], ".local") {
		host = host[:len(host)-6]
	}
	return NormalizeServerName(host)
}

// errNoChosenName is why an identity.json that parses but holds no usable name is refused.
var errNoChosenName = errors.New("identity.json names nothing")

// ReadChosenServerName is the name the owner gave the server; ok is false when there is none (no `identity.json`). The error
// says why a file that cannot be read or names nothing usable is refused; the caller decides what stands in.
func ReadChosenServerName(directory string) (name string, ok bool, err error) {
	text, err := os.ReadFile(identityPath(directory))
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	// The owner's file (a trust boundary): hand edits may break it.
	var stored any
	if err := json.Unmarshal(text, &stored); err != nil {
		return "", false, err
	}
	if object, isObject := stored.(map[string]any); isObject {
		if raw, isText := object["name"].(string); isText {
			if name, ok := NormalizeServerName(raw); ok {
				return name, true, nil
			}
		}
	}
	return "", false, errNoChosenName
}

// WriteChosenServerName saves the owner's name for the server (mode 0600, by a temporary file and a rename), or removes it
// for nil.
func WriteChosenServerName(directory string, name *string) error {
	path := identityPath(directory)
	if name == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	var text bytes.Buffer
	encoder := json.NewEncoder(&text)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "\t")
	if err := encoder.Encode(struct {
		Name string `json:"name"`
	}{*name}); err != nil {
		return err
	}
	if err := os.WriteFile(path+".tmp", text.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}
