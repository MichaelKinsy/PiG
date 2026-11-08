package host

// The `savePrefs` launcher action (p2-tui-port.md sections 3.2, P10): a worker proposes a patch of the visitor's `~/.pi/p2-tui/prefs.json`; the launcher validates it
// (privileged bindings and launcher-only keys such as the editor command are refused before anything is shown), shows the exact change as a diff and writes atomically
// after a local confirmation. pi's own files are never written.
//
// A patch is a JSON object decoded as map[string]any: null is nil, numbers are float64.
//
// pi: apps/p2/src/hosts/tui-prefs-save.ts

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
)

func asRecord(value any) (map[string]any, bool) {
	object, ok := value.(map[string]any)
	return object, ok
}

// ValidatePrefsPatch is a valid patch, or why it is refused (errText). Only `keybindings`, `theme` and whitelisted `settings` keys may change.
func ValidatePrefsPatch(value any) (patch map[string]any, errText string) {
	object, ok := asRecord(value)
	if !ok {
		return nil, "not a patch"
	}
	var extra []string
	for key := range object {
		if key != "keybindings" && key != "theme" && key != "settings" {
			extra = append(extra, key)
		}
	}
	if len(extra) > 0 {
		slices.Sort(extra)
		return nil, "only keybindings, theme and settings can change (not " + strings.Join(extra, ", ") + ")"
	}
	patch = map[string]any{}
	if raw, present := object["keybindings"]; present {
		bindings, ok := asRecord(raw)
		if !ok {
			return nil, "invalid keybindings"
		}
		if len(bindings) > 200 {
			return nil, "too many keybindings"
		}
		ids := make([]string, 0, len(bindings))
		for id := range bindings {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		var privileged []string
		set := map[string]any{}
		for _, id := range ids {
			if prefs.IsPrivileged(id) {
				privileged = append(privileged, id)
			}
			if bindings[id] != nil {
				set[id] = bindings[id]
			}
		}
		if len(privileged) > 0 {
			return nil, "launcher-owned keys are edited only in your own files (" + strings.Join(privileged, ", ") + ")"
		}
		config, invalid := prefs.SanitizeKeybindings(set)
		if len(invalid) > 0 || len(config) != len(set) {
			reason := strings.Join(invalid, ", ")
			if reason == "" {
				reason = "renamed ids"
			}
			return nil, "invalid keybindings (" + reason + ")"
		}
		// pi keeps each binding in the form it arrived in (a string stays a string, a list a list); only the sanitized id set is checked above.
		result := map[string]any{}
		for _, id := range ids {
			if bindings[id] == nil {
				result[id] = nil
			} else if _, kept := config[id]; kept {
				result[id] = bindings[id]
			}
		}
		patch["keybindings"] = result
	}
	if raw, present := object["theme"]; present {
		if raw != nil {
			text, isString := raw.(string)
			if !isString || !prefs.IsThemeSetting(text) {
				return nil, "invalid theme"
			}
		}
		patch["theme"] = raw
	}
	if raw, present := object["settings"]; present {
		settings, ok := asRecord(raw)
		if !ok {
			return nil, "invalid settings"
		}
		// Nulls remove; everything else must survive the whitelist unchanged (no editor command, mouse or paths).
		set := stripNulls(settings)
		sanitized, invalid := prefs.SanitizeSettings(set)
		encoded, _ := json.Marshal(sanitized)
		var round any
		_ = json.Unmarshal(encoded, &round)
		if len(invalid) > 0 || !reflect.DeepEqual(round, dropEmpty(normalize(set))) {
			reason := strings.Join(invalid, ", ")
			if reason == "" {
				reason = "unknown keys"
			}
			return nil, "invalid settings (" + reason + ")"
		}
		patch["settings"] = raw
	}
	return patch, ""
}

func stripNulls(input map[string]any) map[string]any {
	out := map[string]any{}
	for key, item := range input {
		if item == nil {
			continue
		}
		if object, ok := asRecord(item); ok {
			out[key] = stripNulls(object)
		} else {
			out[key] = item
		}
	}
	return out
}

// normalize is the value as JSON round-trips it (numbers become float64, slices []any).
func normalize(value map[string]any) map[string]any {
	encoded, _ := json.Marshal(value)
	var out map[string]any
	_ = json.Unmarshal(encoded, &out)
	return out
}

func dropEmpty(value map[string]any) map[string]any {
	out := map[string]any{}
	for key, item := range value {
		if object, ok := asRecord(item); ok && len(object) == 0 {
			continue
		}
		out[key] = item
	}
	return out
}

// ApplyPrefsPatch applies a patch: null removes, objects merge one level deep (pi's nested settings), empty objects disappear.
func ApplyPrefsPatch(current, patch map[string]any) map[string]any {
	var merge func(base, change map[string]any) map[string]any
	merge = func(base, change map[string]any) map[string]any {
		next := map[string]any{}
		for key, value := range base {
			next[key] = value
		}
		for key, value := range change {
			if value == nil {
				delete(next, key)
			} else if object, ok := asRecord(value); ok {
				baseObject, _ := asRecord(next[key])
				if merged := merge(baseObject, object); len(merged) > 0 {
					next[key] = merged
				} else {
					delete(next, key)
				}
			} else {
				next[key] = value
			}
		}
		return next
	}
	return merge(current, patch)
}

// PrefsDiff is the leaf-by-leaf diff of two preference documents (`- path: old`, `+ path: new`), for the consent screen.
func PrefsDiff(before, after map[string]any) []string {
	var leaves func(value any, path string, out map[string]string)
	leaves = func(value any, path string, out map[string]string) {
		if object, ok := asRecord(value); ok && len(object) > 0 {
			keys := make([]string, 0, len(object))
			for key := range object {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			for _, key := range keys {
				next := key
				if path != "" {
					next = path + "." + key
				}
				leaves(object[key], next, out)
			}
		} else if path != "" {
			encoded, _ := json.Marshal(value)
			out[path] = string(encoded)
		}
	}
	old, next := map[string]string{}, map[string]string{}
	leaves(before, "", old)
	leaves(after, "", next)
	var paths []string
	for path := range old {
		paths = append(paths, path)
	}
	for path := range next {
		if _, ok := old[path]; !ok {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	var lines []string
	for _, path := range paths {
		a, hadOld := old[path]
		b, hasNew := next[path]
		if hadOld == hasNew && a == b {
			continue
		}
		if hadOld {
			lines = append(lines, "- "+path+": "+a)
		}
		if hasNew {
			lines = append(lines, "+ "+path+": "+b)
		}
	}
	return lines
}

// ReadPrefsFile is the `prefs.json` of directory; an absent file is an empty document.
func ReadPrefsFile(directory string) (map[string]any, error) {
	text, err := os.ReadFile(filepath.Join(directory, "prefs.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(text) > prefs.MaxPreferenceFile {
		return nil, errors.New("prefs.json is larger than 256 KiB")
	}
	var value any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(string(text), "\ufeff")), &value); err != nil {
		return nil, err
	}
	object, ok := asRecord(value)
	if !ok {
		return nil, errors.New("prefs.json is not an object")
	}
	return object, nil
}

// WritePrefsFile writes atomically: a fresh 0600 file in the same directory, then a rename over prefs.json.
func WritePrefsFile(directory string, value map[string]any) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	var token [16]byte
	_, _ = rand.Read(token[:])
	temporary := filepath.Join(directory, fmt.Sprintf(".prefs-%s.tmp", hex.EncodeToString(token[:])))
	encoded, err := json.MarshalIndent(value, "", "\t")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(append(encoded, '\n')); err == nil {
		err = file.Close()
	} else {
		_ = file.Close()
	}
	if err == nil {
		err = os.Rename(temporary, filepath.Join(directory, "prefs.json"))
	}
	if err != nil {
		_ = os.Remove(temporary)
	}
	return err
}
