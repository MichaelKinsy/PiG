//go:build linux

package nativeplatform

import "testing"

func TestNativeClipboardUTF8ReplacementSubparts(t *testing.T) {
	// Pi 0.87.1 native clipboard's napi_create_string_utf8 consumes maximal subparts, not whole runs of invalid bytes. Verified through Xvfb/UTF8_STRING in 16-native-x11-clipboard-values.
	for _, tc := range []struct {
		name  string
		input []byte
		want  string
	}{
		{"adjacent invalid starters", []byte{0xff, 0xff}, "\ufffd\ufffd"},
		{"incomplete valid prefix", []byte{0xe1, 0x80}, "\ufffd"},
		{"surrogate encoding", []byte{0xed, 0xa0, 0x80}, "\ufffd\ufffd\ufffd"},
		{"overlong encoding", []byte{0xc0, 0xaf}, "\ufffd\ufffd"},
		{"BOM retained", []byte{0xef, 0xbb, 0xbf}, "\ufeff"},
		{"mixed native probe", []byte{0xff, 0xff, 0x61, 0xe1, 0x80, 0x62, 0xed, 0xa0, 0x80, 0xc0, 0xaf}, "\ufffd\ufffda\ufffdb\ufffd\ufffd\ufffd\ufffd\ufffd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, available, err := clipboardTextValue(clipboardReadResult{data: tc.input, available: true})
			if err != nil || !available || got == nil || *got != tc.want {
				t.Fatalf("value=%v available=%t err=%v want=%q", got, available, err, tc.want)
			}
		})
	}
}
