package sendfile

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

// Ports send-file.test.ts "previewText keeps UTF-8 text, drops a code point cut by a partial read, refuses NUL".
func TestPreviewTextKeepsUTF8TextDropsACodePointCutByAPartialReadRefusesNUL(t *testing.T) {
	text := func(content []byte, partial bool) *string {
		shown, ok := PreviewText(content, partial)
		if !ok {
			return nil
		}
		return &shown
	}
	if got := text([]byte("héllo"), false); got == nil || *got != "héllo" {
		t.Errorf("UTF-8 text: %v", got)
	}
	cut := []byte("ab€")[:4]
	if got := text(cut, true); got == nil || *got != "ab" {
		t.Errorf("a cut code point of a partial read: %v", got)
	}
	if got := text(cut, false); got != nil {
		t.Errorf("a cut code point of a whole file: %q", *got)
	}
	if got := text([]byte{0x61, 0x00}, false); got != nil {
		t.Errorf("NUL: %q", *got)
	}
	// What a TextDecoder does besides: the byte order mark is not part of the text, and nothing is text of an empty read.
	if got := text([]byte("\xef\xbb\xbfabc"), false); got == nil || *got != "abc" {
		t.Errorf("byte order mark: %v", got)
	}
	if got := text(nil, false); got == nil || *got != "" {
		t.Errorf("empty: %v", got)
	}
	// Only up to 3 bytes are dropped, and only of a partial read.
	if got := text([]byte("ab\xff\xff\xff\xff"), true); got != nil {
		t.Errorf("invalid bytes: %q", *got)
	}
}

func TestTheToolDescriptionIsUpstreams(t *testing.T) {
	source, err := os.ReadFile("../../web/upstream/plugins/send-file/tool.ts")
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`description:\s*("(?:[^"\\]|\\.)*")`).FindSubmatch(source)
	if match == nil {
		t.Fatal("no description in upstream's tool.ts")
	}
	if unquoted, err := strconv.Unquote(string(match[1])); err != nil || unquoted != toolDescription {
		t.Errorf("the tool's description differs from upstream's (%v):\n%s", err, unquoted)
	}
}
