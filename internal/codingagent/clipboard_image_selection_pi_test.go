package codingagent

// pi: packages/coding-agent/src/utils/clipboard-image.ts

import (
	"slices"
	"testing"
)

// isWaylandSession (clipboard-image.ts:22-24) is true for WAYLAND_DISPLAY or XDG_SESSION_TYPE === "wayland", and readClipboardImage
// (clipboard-image.ts:215-216) reads wl-paste first in a Wayland session. Pi's clipboard-image tests set only WAYLAND_DISPLAY.
func TestClipboardImageXDGWaylandSessionReadsWlPasteFirst(t *testing.T) {
	for _, tc := range []struct {
		session string
		want    []string
	}{
		{"wayland", []string{"wl-paste", "wl-paste"}},
		{"x11", []string{"xclip", "xclip"}},
		{"Wayland", []string{"xclip", "xclip"}},
	} {
		t.Run(tc.session, func(t *testing.T) {
			f := newClipboardImageFixture(t, "linux", map[string]string{"XDG_SESSION_TYPE": tc.session, "DISPLAY": ":0"})
			f.command = func(_ string, args []string) ([]byte, error) {
				if slices.Contains(args, "--list-types") || slices.Contains(args, "TARGETS") {
					return []byte("image/png\n"), nil
				}
				return upstreamClipboardPNG, nil
			}
			assertClipboardImage(t, upstreamClipboardPNG)
			if !slices.Equal(f.commands, tc.want) {
				t.Fatalf("commands = %v, want %v", f.commands, tc.want)
			}
		})
	}
}

// selectPreferredImageMimeType (clipboard-image.ts:45-60) trims the advertised types, takes the first one whose base type is png, then
// jpeg, webp and gif in that order regardless of the advertised order, falls back to the first image/* type, and keeps the advertised
// spelling; the read reports the base type (clipboard-image.ts:110).
func TestClipboardImageSelectsPiPreferredType(t *testing.T) {
	for _, tc := range []struct {
		name, listing, requested, mime string
	}{
		{"png wins over earlier types", "image/gif\nimage/webp\nimage/jpeg\n  IMAGE/PNG; charset=x \n", "IMAGE/PNG; charset=x", "image/png"},
		{"jpeg before webp and gif", "image/gif\r\nimage/webp\r\nimage/jpeg\r\n", "image/jpeg", "image/jpeg"},
		{"webp before gif", "text/plain\nimage/gif\nimage/webp\n", "image/webp", "image/webp"},
		// The bytes are not a TIFF image, so the PNG conversion fails and the read returns nothing (clipboard-image.ts:231-234).
		{"first other image type", "text/plain\nimage/tiff\nimage/bmp\n", "image/tiff", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newClipboardImageFixture(t, "linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0"})
			var requested []string
			f.command = func(_ string, args []string) ([]byte, error) {
				if slices.Contains(args, "--list-types") {
					return []byte(tc.listing), nil
				}
				requested = append(requested, args[1])
				return upstreamClipboardPNG, nil
			}
			data, mime, err := ReadClipboardImageContext(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(requested, []string{tc.requested}) {
				t.Fatalf("wl-paste --type %q, want %q", requested, tc.requested)
			}
			if wantData := upstreamClipboardPNG; tc.mime == "" {
				if data != nil || mime != "" {
					t.Fatalf("image = %v %q, want nothing", data, mime)
				}
			} else if !slices.Equal(data, wantData) || mime != tc.mime {
				t.Fatalf("image = %v %q, want the bytes as %q", data, mime, tc.mime)
			}
		})
	}
}
