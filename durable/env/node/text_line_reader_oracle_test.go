package node

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// Ports packages/durable/src/env/node.ts NodeTextLineReader (:388-448, openTextLineReader at :896): the pinned upstream NodeExecutionEnv reads the same files under Node, and the Go TextLineReader returns the same lines
// (text and terminated flag) for each: an LF-strict split, a leading byte-order mark dropped only at the start of the file, invalid and truncated UTF-8 as U+FFFD, a character straddling the 64 KiB read chunk, and an unterminated last line.
// mutation-checked: splitting on CRLF, marking the last line terminated, dropping no leading mark, and dropping the mark at every chunk start each fail it.
func TestTextLineReaderMatchesPi(t *testing.T) {
	big := strings.Repeat("a", 64*1024-2) + "😀tail\n" + strings.Repeat("é", 70_000)
	files := map[string][]byte{
		"empty":        {},
		"newline":      []byte("\n"),
		"two newlines": []byte("\n\n"),
		"unterminated": []byte("one\ntwo"),
		"terminated":   []byte("one\ntwo\n"),
		"crlf":         []byte("one\r\ntwo\r\n"),
		"bom":          []byte("\xef\xbb\xbfone\ntwo"),
		"bom only":     []byte("\xef\xbb\xbf"),
		"double bom":   []byte("\xef\xbb\xbf\xef\xbb\xbfone"),
		"bom mid line": []byte("a\xef\xbb\xbfb\n\xef\xbb\xbfc"),
		"bad after":    []byte("\xe2\x82\xef\xbb\xbfa\n"),
		"truncated":    []byte("ok\n\xf0\x9f"),
		"stray":        []byte("a\x80b\xffc\n"),
		"large":        []byte(big),
		"bom at chunk": []byte(strings.Repeat("a", 64*1024) + "\xef\xbb\xbfb\n"),
		"bom in chunk": []byte(strings.Repeat("a", 64*1024-1) + "\xef\xbb\xbfb\n"),
	}
	pieces := [][]byte{[]byte("\n"), []byte("a"), []byte("\xef\xbb\xbf"), []byte("\xc3\xa9"), []byte("\xf0\x9f\x98\x80"), []byte("\xe2\x82"), []byte("\x80"), []byte("\xff"), []byte("\r\n"), []byte("\xe2\x82\xac")}
	for seed := range 200 {
		state := uint32(seed + 1)
		next := func() int {
			state = state*1664525 + 1013904223
			return int(state >> 16)
		}
		var file []byte
		for range next() % 50 {
			file = append(file, pieces[next()%len(pieces)]...)
		}
		files["random "+strconv.Itoa(seed)] = file
	}
	env, root := newTestEnv(t)
	names := make([]string, 0, len(files))
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	input, err := json.Marshal(map[string]any{"dir": root, "files": names})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/text_line_reader.mjs", repo)
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v", err)
	}
	var want map[string][]durableenv.TextLine
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		reader := must(env.OpenTextLineReader(background, name))
		got := readAllLines(t, reader)
		_ = reader.Close(background)
		if len(got) != len(want[name]) {
			t.Errorf("%s: Go %d lines, Pi %d", name, len(got), len(want[name]))
			continue
		}
		for i := range got {
			if got[i] != want[name][i] {
				t.Errorf("%s line %d: Go %+v, Pi %+v", name, i, got[i], want[name][i])
				break
			}
		}
	}
}
