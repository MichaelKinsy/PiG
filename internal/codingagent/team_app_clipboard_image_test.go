package codingagent

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

// Fake every clipboard operation, including WSL detection: neither the host
// clipboard nor /proc/version participates. Package hooks are fully restored.
func clipboardImageEvidenceBackend(t *testing.T, run clipboardRunner) {
	t.Helper()
	oldRun, oldEnv, oldRead, oldOS := clipboardRun, clipboardEnv, clipboardReadFile, clipboardGOOS
	t.Cleanup(func() {
		clipboardRun, clipboardEnv, clipboardReadFile, clipboardGOOS = oldRun, oldEnv, oldRead, oldOS
	})
	clipboardRun = run
	clipboardEnv = func(key string) string {
		return map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"}[key]
	}
	clipboardReadFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	clipboardGOOS = "linux"
}

// Upstream clipboard-image.ts uses undefined for failure and null for empty.
// Only failure may fall through to stale X11; preserve MIME parameters for the
// backend argument while returning a canonical MIME to the paste caller.
func TestClipboardImageBackendResults(t *testing.T) {
	for _, tc := range []struct {
		name, types      string
		listErr, readErr error
		empty            bool
		wantCommands     []string
		wantMIME         string
	}{
		{"MIME preference and parameters", "image/gif\r\nimage/jpeg\r\n IMAGE/PNG; variant=clipboard \r\nimage/webp\n", nil, nil, false, []string{"wl-paste --list-types", "wl-paste --type IMAGE/PNG; variant=clipboard --no-newline"}, "image/png"},
		{"no advertised image", "text/plain\r\n", nil, nil, false, []string{"wl-paste --list-types"}, ""},
		{"empty advertised image", "image/png\n", nil, nil, true, []string{"wl-paste --list-types", "wl-paste --type image/png --no-newline"}, ""},
		{"listing failure falls back", "", errors.New("backend failed"), nil, false, []string{"wl-paste --list-types", "xclip -selection clipboard -t TARGETS -o", "xclip -selection clipboard -t image/png -o"}, "image/png"},
		{"transfer failure falls back", "image/png\n", nil, errors.New("transfer failed"), false, []string{"wl-paste --list-types", "wl-paste --type image/png --no-newline", "xclip -selection clipboard -t TARGETS -o", "xclip -selection clipboard -t image/png -o"}, "image/png"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte("\x89PNG\r\n\x1a\nclipboard sentinel")
			var commands []string
			clipboardImageEvidenceBackend(t, func(_ context.Context, name string, args ...string) ([]byte, error) {
				commands = append(commands, name+" "+strings.Join(args, " "))
				switch {
				case name == "wl-paste" && slices.Contains(args, "--list-types"):
					return []byte(tc.types), tc.listErr
				case name == "wl-paste":
					if tc.empty {
						return nil, nil
					}
					return body, tc.readErr
				case name == "xclip" && slices.Contains(args, "TARGETS"):
					return []byte("image/png\n"), nil
				case name == "xclip":
					return body, nil
				default:
					t.Fatalf("unexpected command: %s %q", name, args)
					return nil, nil
				}
			})
			data, mime, err := ReadClipboardImageContext(t.Context())
			if err != nil || mime != tc.wantMIME {
				t.Errorf("result MIME=%q error=%v, want %q/no error", mime, err, tc.wantMIME)
			}
			want := body
			if tc.wantMIME == "" {
				want = nil
			}
			if !bytes.Equal(data, want) {
				t.Errorf("bytes = %q, want %q", data, want)
			}
			if !slices.Equal(commands, tc.wantCommands) {
				t.Errorf("backend calls = %q, want %q", commands, tc.wantCommands)
			}
		})
	}
}
