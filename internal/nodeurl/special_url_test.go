package nodeurl

import "testing"

func TestSpecialURLHostSharesFileHostNormalization(t *testing.T) {
	for _, tc := range []struct{ input, network, file string }{
		{"münich.example", "xn--mnich-kva.example", "xn--mnich-kva.example"},
		{"LOCALHOST", "localhost", ""},
		{"%65xample.com", "example.com", "example.com"},
		{"0x7f.1", "127.0.0.1", "127.0.0.1"},
		{"[::ffff:192.0.2.128]", "[::ffff:c000:280]", "[::ffff:c000:280]"},
	} {
		if got, err := SpecialHost(tc.input); err != nil || got != tc.network {
			t.Errorf("SpecialHost(%q)=%q,%v want=%q", tc.input, got, err, tc.network)
		}
		if got, err := FileHost(tc.input); err != nil || got != tc.file {
			t.Errorf("FileHost(%q)=%q,%v want=%q", tc.input, got, err, tc.file)
		}
	}
}

func TestSpecialURLPathPreservesProtocolDriveRules(t *testing.T) {
	for _, tc := range []struct{ input, network, file string }{
		{"/a/%2e%2e/b", "/b", "/b"},
		{"/a/.%2E/b", "/b", "/b"},
		{"/a/%2e./b", "/b", "/b"},
		{"/a/%2e/b", "/a/b", "/a/b"},
		{`/a\b`, "/a/b", "/a/b"},
		{"/a%2Fb", "/a%2Fb", "/a%2Fb"},
		{"/C:/../x", "/x", "/C:/x"},
		{"/C|/x", "/C|/x", "/C:/x"},
	} {
		if got := SpecialPath(tc.input, false); got != tc.network {
			t.Errorf("network path(%q)=%q want=%q", tc.input, got, tc.network)
		}
		if got := SpecialPath(tc.input, true); got != tc.file {
			t.Errorf("file path(%q)=%q want=%q", tc.input, got, tc.file)
		}
	}
}

// Node's `new URL("https://xn--.example")` throws (measured on Node v24.19): a label that is only the xn-- prefix has no punycode to decode.
func TestSpecialHostRejectsAnEmptyPunycodeLabel(t *testing.T) {
	for _, host := range []string{"xn--.example", "XN--", "a.xn--", "a.xn--.b"} {
		if got, err := SpecialHost(host); err == nil {
			t.Errorf("SpecialHost(%q) = %q, want an error", host, got)
		}
	}
	if got, err := SpecialHost("xn--mnich-kva.example"); err != nil || got != "xn--mnich-kva.example" {
		t.Errorf("SpecialHost of a valid punycode label = %q, %v", got, err)
	}
}
