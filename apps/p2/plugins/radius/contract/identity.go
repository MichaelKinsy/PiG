package contract

// The brain's name: what this p2 calls itself, in `<data>/identity.json`.
//
// The name belongs to the brain, not to Radius: it lives in the data directory beside the sessions, works without Radius, and survives
// `radius:login` and signing out (which rewrite or remove `radius.json`). The first read creates it with the device's name ("Cristina's
// MacBook Pro"), so a brain usually has one without asking; when the device has no usable name, the brain has none and Radius shows its server
// ID. The brain reports it to Radius on every host connect (`?name=`, see brain.go), and Radius records it on the claim, so a rename here
// reaches Radius with the next reconnect; nothing is pushed from Radius. Two brains on one machine start with the same name; rename one by
// editing the file.
//
// The name is a label, never an identity or a credential: the server ID stays the brain's identity, and nothing authorizes by name.
//
// Ports apps/p2/plugins/radius/identity.ts.

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
)

// BrainIdentity is identity.json.
type BrainIdentity struct {
	Name string `json:"name"`
}

// maxBrainNameLength is the longest name, in characters, as Radius stores (`MAX_SERVER_NAME_LENGTH`): a DNS label (63), so a host name is
// never cut. Linux 64, Windows 15, Kubernetes and cloud VMs 63; only a macOS computer name can be longer.
const maxBrainNameLength = 63

// BrainIdentityPath is identity.json in the data directory.
func BrainIdentityPath(directory string) string { return filepath.Join(directory, "identity.json") }

// NormalizeBrainName is name as one trimmed line without control characters, at most 63 characters; ok is false if it is empty.
func NormalizeBrainName(name string) (string, bool) {
	line := strings.Join(strings.FieldsFunc(strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) {
			return rune(0x20)
		}
		return r
	}, name), unicode.IsSpace), " ")
	if runes := []rune(line); len(runes) > maxBrainNameLength {
		line = strings.TrimSpace(string(runes[:maxBrainNameLength]))
	}
	return line, line != ""
}

// DeviceName is the device's name, best effort: on macOS the computer name from System Settings, else the host name the operating system
// reports, without the `.local` that macOS adds to it. Empty when neither gives one; the brain then has no name and Radius shows its server ID.
func DeviceName() string {
	if runtime.GOOS == "darwin" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "scutil", "--get", "ComputerName").Output(); err == nil {
			if name, ok := NormalizeBrainName(string(out)); ok {
				return name
			}
		}
	}
	host, err := os.Hostname()
	if err != nil {
		return ""
	}
	if len(host) >= len(".local") && strings.EqualFold(host[len(host)-len(".local"):], ".local") {
		host = host[:len(host)-len(".local")]
	}
	name, _ := NormalizeBrainName(host)
	return name
}

// ReadBrainIdentity is the brain's identity, created with the device's name when absent. A file that is not JSON or names nothing usable is
// replaced the same way, so a broken edit falls back to the device name instead of an unnamed brain.
//
// Best effort, never fails: the name is a label, and a data directory the brain cannot read or write it in (a read-only or locked-down sandbox,
// `identity.json` a directory) must not keep the brain offline. Then the device's name stands in for this read without being saved, and a file
// that exists but cannot be read is left alone. ok is false when there is no name at all (no usable file and no device name): nothing is saved,
// so a later read tries the device again.
func ReadBrainIdentity(directory string) (identity BrainIdentity, ok bool) {
	named := func(name string) (BrainIdentity, bool) { return BrainIdentity{Name: name}, name != "" }
	raw, err := os.ReadFile(BrainIdentityPath(directory))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return named(DeviceName())
	}
	if err == nil {
		var stored struct {
			Name any `json:"name"`
		}
		if json.Unmarshal(raw, &stored) == nil {
			if text, isText := stored.Name.(string); isText {
				if name, usable := NormalizeBrainName(text); usable {
					return BrainIdentity{Name: name}, true
				}
			}
		}
	}
	identity, ok = named(DeviceName())
	if ok {
		_ = WriteBrainIdentity(directory, identity)
	}
	return identity, ok
}

// WriteBrainIdentity saves identity.json atomically (0600).
func WriteBrainIdentity(directory string, identity BrainIdentity) error {
	path := BrainIdentityPath(directory)
	data, err := json.MarshalIndent(identity, "", "\t")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path+".tmp", append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}
