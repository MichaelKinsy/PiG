package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/coding-agent/src/core/tools/grep.ts:176-299

// grep with context formats limit x (2*context+1) lines and keeps the first 50KB. PiG measures the lines past those 50KB instead of building them (#9276); the text, the truncation details, the match-limit notice and linesTruncated, which a cut line far past the head still sets, must equal Pi's.
func TestGrepContextOutputMatchesPi(t *testing.T) {
	rg := requireRG(t)
	dir := t.TempDir()
	var file strings.Builder
	for i := range 3000 {
		switch {
		case i == 2500:
			// A line over GREP_MAX_LINE_LENGTH only a large context reaches, long after the output passed 50KB.
			file.WriteString("context " + strings.Repeat("日本🙂", 300) + "\r\n")
		case i%7 == 0:
			file.WriteString("match " + strconv.Itoa(i) + " é\n")
		default:
			file.WriteString("line " + strconv.Itoa(i) + "\n")
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(file.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "small.txt"), []byte("match a\nb\nmatch "+strings.Repeat("x", 700)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Match lines just under GREP_MAX_LINE_LENGTH but about 1KB each, so a search without context passes 50KB near match 52 and the later lines are measured; match 80 is the only line over the limit, so only a measured line sets linesTruncated.
	var wide strings.Builder
	for i := range 120 {
		width := 490
		if i == 80 {
			width = 600
		}
		wide.WriteString("wide " + strconv.Itoa(i) + " " + strings.Repeat("é", width) + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "wide.txt"), []byte(wide.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []map[string]any{
		{"pattern": "match", "path": "small.txt"},
		{"pattern": "match", "path": "small.txt", "context": 1},
		{"pattern": "match", "path": "big.txt"},
		{"pattern": "match", "path": "big.txt", "context": 2, "limit": 50},
		{"pattern": "match", "path": "big.txt", "context": 1.5, "limit": 30},
		{"pattern": "match", "path": "big.txt", "context": 400, "limit": 5},
		{"pattern": "match", "path": "big.txt", "context": 3000, "limit": 100},
		// Matches past line 401, so a fractional context starts each block at a fractional line number ("big.txt-1601.5- ") and those lines are measured past the head.
		{"pattern": "match 2[0-9]{3} ", "path": "big.txt", "context": 400.5, "limit": 5},
		{"pattern": "wide", "path": "wide.txt"},
		// A directory search: rg walks files in parallel and Pi does not sort, so only one file may match or the file order, and the head, would vary between runs.
		{"pattern": "match [0-9]+ é", "context": 3000},
	}
	input, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	agentDir := t.TempDir()
	cmd := exec.CommandContext(t.Context(), "node", "testdata/grep.mjs", pigversion.UpstreamPackageVersion, dir)
	cmd.Env = append(os.Environ(), "PI_CODING_AGENT_DIR="+agentDir, "PI_OFFLINE=1", "HOME="+agentDir)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []struct {
		Text    string `json:"text"`
		Details any    `json:"details"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	for i, params := range cases {
		args, _ := json.Marshal(params)
		result, err := (&GrepTool{CWD: dir, RgPath: rg}).Execute(context.Background(), "", args, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := result.Text(); got != expected[i].Text {
			t.Errorf("%v: text differs from Pi (%d vs %d bytes)\n got tail %q\nwant tail %q", params, len(got), len(expected[i].Text), tail(got), tail(expected[i].Text))
		}
		var details any
		if result.Details != nil {
			encoded, err := json.Marshal(result.Details)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(encoded, &details); err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(details, expected[i].Details) {
			t.Errorf("%v: details = %v, Pi %v", params, details, expected[i].Details)
		}
	}
}

func tail(s string) string { return s[max(0, len(s)-200):] }
