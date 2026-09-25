package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// utf8StreamDecoder matches TextDecoder: a split character waits for the
// next chunk and each maximal invalid subpart becomes one U+FFFD.
func TestUTF8StreamDecoderMatchesTextDecoder(t *testing.T) {
	var d utf8StreamDecoder
	if got := d.decode([]byte("a\xe2\x82"), true); got != "a" {
		t.Fatalf("partial = %q", got)
	}
	if got := d.decode([]byte("\xacb"), true); got != "€b" {
		t.Fatalf("completed = %q", got)
	}
	for in, want := range map[string]string{
		"\xe2\x82a":          "�a",
		"\xc0\x80":           "��",
		"\xed\xa0\x80":       "���",
		"\xf0\x9f\x98":       "�",
		"\xff":               "�",
		"ok\xf0\x9f\x98\x80": "ok😀",
	} {
		var d utf8StreamDecoder
		if got := d.decode([]byte(in), false); got != want {
			t.Errorf("decode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOutputAccumulatorKeepsCharactersSplitAcrossChunks(t *testing.T) {
	acc := NewOutputAccumulator("pi-test")
	data := []byte(strings.Repeat("a", 4095) + strings.Repeat("€", 50))
	acc.Append(data[:4096]) // ends inside the first "€"
	acc.Append(data[4096:])
	acc.Finish()
	snap := acc.Snapshot(false)
	if strings.ContainsRune(snap.Content, '�') || !strings.HasSuffix(snap.Content, strings.Repeat("€", 50)) {
		t.Fatalf("content corrupted: %q", snap.Content[4000:])
	}
}

// Raw bytes go to the full-output file, as upstream writes the chunks.
func TestOutputAccumulatorTempFileHoldsRawBytes(t *testing.T) {
	acc := newOutputAccumulator(2000, 10, "pi-test")
	acc.Append([]byte("\x1b[31mred\x1b[0m and more\n"))
	acc.Finish()
	snap := acc.Snapshot(true)
	if err := acc.CloseTempFile(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(snap.FullOutputPath) })
	if !strings.HasPrefix(filepath.Base(snap.FullOutputPath), "pi-test-") {
		t.Fatalf("path = %q", snap.FullOutputPath)
	}
	got, err := os.ReadFile(snap.FullOutputPath)
	if err != nil || string(got) != "\x1b[31mred\x1b[0m and more\n" {
		t.Fatalf("temp file = %q, %v", got, err)
	}
}

// Multi-byte output larger than one read never gains U+FFFD, on either the
// tool path or user bash.
func TestBashMultibyteOutputAcrossReads(t *testing.T) {
	dir := t.TempDir()
	line := strings.Repeat("€", 30) + "\n"
	file := filepath.Join(dir, "euro.txt")
	if err := os.WriteFile(file, []byte(strings.Repeat("a", 4095)+strings.Repeat(line, 1000)), 0o644); err != nil {
		t.Fatal(err)
	}
	bt := &BashTool{CWD: dir}
	args, _ := json.Marshal(bashParams{Command: "cat euro.txt"})
	res, err := bt.Execute(context.Background(), "", args, nil)
	if err != nil || res.IsError {
		t.Fatalf("execute: %v %+v", err, res)
	}
	if n := strings.Count(res.Content, "�"); n != 0 {
		t.Fatalf("tool output has %d U+FFFD", n)
	}
	sh, err := defaultShellConfig()
	if err != nil {
		t.Fatal(err)
	}
	ub, err := ExecuteBash(context.Background(), "cat euro.txt", dir, sh, BashExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(ub.Output, "�"); n != 0 {
		t.Fatalf("user bash output has %d U+FFFD", n)
	}
}

// CDT-002: TextDecoder (default ignoreBOM: false) drops a leading BOM, even
// split across chunks; upstream's OutputAccumulator gives content "x",
// totalBytes 1 (probed under Node). Only the first code point counts.
func TestOutputAccumulatorStripsInitialBOMLikeTextDecoder(t *testing.T) {
	a := NewOutputAccumulator("pi-test")
	a.Append([]byte{0xef})
	a.Append([]byte{0xbb})
	a.Append([]byte{0xbf, 'x'})
	a.Finish()
	if got := a.Snapshot(false); got.Content != "x" || got.Truncation.TotalBytes != 1 {
		t.Fatalf("snapshot = %+v", got)
	}
	later := NewOutputAccumulator("pi-test")
	later.Append([]byte("x\xef\xbb\xbfy"))
	later.Finish()
	if got := later.Snapshot(false).Content; got != "x\uFEFFy" {
		t.Fatalf("a BOM after the first code point must stay: %q", got)
	}
}

// User bash decodes with TextDecoder too; file reads use Buffer.toString,
// which keeps the BOM.
func TestBOMHandlingByPath(t *testing.T) {
	sh, err := defaultShellConfig()
	if err != nil {
		t.Fatal(err)
	}
	res, err := ExecuteBash(context.Background(), `printf '\357\273\277hi'`, t.TempDir(), sh, BashExecOptions{})
	if err != nil || res.Output != "hi" {
		t.Fatalf("user bash output = %q, %v", res.Output, err)
	}
	read := readFileTool(t, "bom.txt", []byte("\xef\xbb\xbfhello"), map[string]any{})
	if read.Content != "\uFEFFhello" {
		t.Fatalf("read content = %q, want the BOM kept", read.Content)
	}
}
