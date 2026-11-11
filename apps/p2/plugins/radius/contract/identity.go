package contract

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

// Ports apps/p2/plugins/radius/identity.ts: the brain's name, what this p2 calls itself, in `<data>/identity.json`.
//
// The name belongs to the brain, not to Radius: it lives in the data directory beside the sessions, works without Radius,
// and survives `radius:login` and signing out (which rewrite or remove `radius.json`). The first read creates it with the
// device's name ("Cristina's MacBook Pro"), so a brain usually has one without asking; when the device has no usable name,
// the brain has none and Radius shows its server ID. The brain reports it to Radius on every host connect (`?name=`, see
// brain.go), and Radius records it on the claim, so a rename here reaches Radius with the next reconnect; nothing is pushed
// from Radius. Two brains on one machine start with the same name; rename one by editing the file.
//
// The name is a label, never an identity or a credential: the server ID stays the brain's identity, and nothing authorizes
// by name.

// BrainIdentity is the content of identity.json.
type BrainIdentity struct {
	Name string `json:"name"`
}

// MaxNameLength is the longest name, in characters, as Radius stores (`MAX_SERVER_NAME_LENGTH`): a DNS label (63), so a host
// name is never cut. Linux 64, Windows 15, Kubernetes and cloud VMs 63; only a macOS computer name can be longer.
const MaxNameLength = 63

// IdentityPath is the file of the brain's identity in directory.
func IdentityPath(directory string) string { return filepath.Join(directory, "identity.json") }

// isScriptSpace is JavaScript's `\s` and `String.prototype.trim` white space (Go's unicode.IsSpace differs at U+0085 and
// U+FEFF).
func isScriptSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// NormalizeBrainName is name as one trimmed line without control characters, at most MaxNameLength characters; ok is false
// if nothing is left.
func NormalizeBrainName(name string) (string, bool) {
	var line strings.Builder
	space := false
	for _, r := range name {
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) || isScriptSpace(r) {
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
	if len(clipped) > MaxNameLength {
		clipped = clipped[:MaxNameLength]
	}
	// Clipping may leave a space at the end; the one before the first character was never written.
	result := strings.TrimRightFunc(string(clipped), isScriptSpace)
	return result, result != ""
}

// DeviceName is the device's name, best effort: on macOS the computer name from System Settings ("Cristina’s MacBook Air"),
// else the host name the operating system reports, without the `.local` that macOS adds to it. ok is false when neither
// gives one; the brain then has no name and Radius shows its server ID.
func DeviceName() (name string, ok bool) {
	if runtime.GOOS == "darwin" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if stdout, err := exec.CommandContext(ctx, "scutil", "--get", "ComputerName").Output(); err == nil {
			if name, ok := NormalizeBrainName(string(stdout)); ok {
				return name, true
			}
		}
	}
	host, err := os.Hostname()
	if err != nil {
		return "", false
	}
	if len(host) >= 6 && strings.EqualFold(host[len(host)-6:], ".local") {
		host = host[:len(host)-6]
	}
	return NormalizeBrainName(host)
}

// ReadBrainIdentity is the brain's identity, created with the device's name when absent. A file that is not JSON or names
// nothing usable is replaced the same way, so a broken edit falls back to the device name instead of an unnamed brain.
//
// Best effort, never fails: the name is a label, and a data directory the brain cannot read or write it in (a read-only or
// locked-down sandbox, `identity.json` a directory) must not keep the brain offline. Then the device's name stands in for
// this read without being saved, and a file that exists but cannot be read is left alone. Nil when there is no name at all
// (no usable file and no device name): nothing is saved, so a later read tries the device again.
func ReadBrainIdentity(directory string) *BrainIdentity {
	text, err := os.ReadFile(IdentityPath(directory))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return named(DeviceName())
	}
	var stored struct {
		Name any `json:"name"`
	}
	if err == nil {
		// Not an object (null, an array, a string): no name, as for invalid JSON.
		_ = json.Unmarshal(text, &stored)
	}
	if name, isText := stored.Name.(string); isText {
		if name, ok := NormalizeBrainName(name); ok {
			return &BrainIdentity{Name: name}
		}
	}
	identity := named(DeviceName())
	if identity != nil {
		_ = WriteBrainIdentity(directory, *identity)
	}
	return identity
}

func named(name string, ok bool) *BrainIdentity {
	if !ok {
		return nil
	}
	return &BrainIdentity{Name: name}
}

// WriteBrainIdentity saves identity (mode 0600), by a temporary file and a rename.
func WriteBrainIdentity(directory string, identity BrainIdentity) error {
	path := IdentityPath(directory)
	var text bytes.Buffer
	encoder := json.NewEncoder(&text)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "\t")
	if err := encoder.Encode(identity); err != nil {
		return err
	}
	if err := os.WriteFile(path+".tmp", text.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}
