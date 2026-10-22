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

// Node's `new URL("https://xn--.example")` is valid in Node 24.22.1 and 26.12.1 (ada 4), which only lowercases an ASCII host and never decodes or validates its "xn--" labels; Node 22.19 and 24.19 (ada 2 and 3) throw for it. A host with a non-ASCII character still goes through UTS #46, and ada rejects its label that is only the xn-- prefix.
func TestSpecialHostKeepsAnASCIIPunycodeLabelAsIs(t *testing.T) {
	for host, want := range map[string]string{"xn--.example": "xn--.example", "XN--": "xn--", "a.xn--": "a.xn--", "a.XN--a.b": "a.xn--a.b", "xn--mnich-kva.example": "xn--mnich-kva.example"} {
		if got, err := SpecialHost(host); err != nil || got != want {
			t.Errorf("SpecialHost(%q) = %q, %v, want %q", host, got, err, want)
		}
	}
	for _, host := range []string{"xn--.é", "é.xn--", "xn--é"} {
		if got, err := SpecialHost(host); err == nil {
			t.Errorf("SpecialHost(%q) = %q, want an error", host, got)
		}
	}
}
