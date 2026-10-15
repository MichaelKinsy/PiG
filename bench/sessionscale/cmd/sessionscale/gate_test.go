// SPDX-License-Identifier: MIT

//go:build linux

package main

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/bench/sessionscale"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The session-scale gates, on the release binary (opt-in: SESSIONSCALE_GATE=1, about three minutes and 300 MB of disk):
//   - resident memory after resuming the 300 MB owner-shape fixture is at most 80 MiB + 10 x the active context's bytes on disk + 400 B per
//     entry of the file + twice the /tree search text of its messages (role and the first 200 characters, which the index keeps so a search
//     reads no entry), so it follows the context window, not the history;
//   - the median CPU of a prompt on that fixture is at most twice, or 50 ms above, the median on a 1k-entry fixture of the same shape.
func TestResumeMemoryAndPromptCostFollowTheContext(t *testing.T) {
	if os.Getenv("SESSIONSCALE_GATE") != "1" {
		t.Skip("set SESSIONSCALE_GATE=1 to run the session-scale gates")
	}
	root := testenv.ModuleRoot(t)
	dir := t.TempDir()
	pig := filepath.Join(dir, "pig")
	build := exec.Command("go", "build", "-o", pig, "./cmd/pig")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build pig: %v\n%s", err, out)
	}
	var searchBytes int64
	measure := func(name string, bytes int64) (sessionscale.Meta, map[string]point) {
		fixture := filepath.Join(dir, name)
		meta, err := sessionscale.Write(fixture, sessionscale.Shape{Bytes: bytes, Seed: 1}, "")
		if err != nil {
			t.Fatal(err)
		}
		searchBytes = fixtureSearchTextBytes(t, fixture)
		out := filepath.Join(dir, name+".jsonl")
		if err := run([]string{"-arm", "pig", "-cmd", pig + " -ne", "-fixture", fixture, "-prompts", "10", "-out", out, "-timeout", "20m"}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		f, err := os.Open(out)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		points := map[string]point{}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var p point
			if json.Unmarshal(sc.Bytes(), &p) == nil && p.Label != "" {
				points[p.Label] = p
			}
		}
		return meta, points
	}
	median := func(values []int64) int64 {
		sorted := slices.Clone(values)
		slices.Sort(sorted)
		return sorted[len(sorted)/2]
	}
	_, small := measure("f1k", 6500<<10)
	meta, large := measure("f299", sessionscale.OwnerBytes)
	resume := large["resume"]
	bound := int64(80<<20+10*meta.ContextBytes+400*int64(meta.Entries)+2*searchBytes) >> 10
	t.Logf("300 MB fixture: %d entries, active context %d bytes, search text %d bytes; RSS after resume %d KiB, bound %d KiB", meta.Entries, meta.ContextBytes, searchBytes, resume.RssKiB, bound)
	if resume.RssKiB == 0 || resume.RssKiB > bound {
		t.Errorf("RSS after resume %d KiB exceeds the context-proportional bound %d KiB", resume.RssKiB, bound)
	}
	smallCPU, largeCPU := median(small["prompts-10"].PromptCPUms), median(large["prompts-10"].PromptCPUms)
	t.Logf("median prompt CPU: 1k entries %d ms, %d entries %d ms", smallCPU, meta.Entries, largeCPU)
	testenv.TimingGate(t, largeCPU > max(2*smallCPU, smallCPU+50), "a prompt costs %d ms at %d entries and %d ms at 1k: per-prompt cost grows with history", largeCPU, meta.Entries, smallCPU)
}

// fixtureSearchTextBytes sums, over the message records of a fixture's session file, the bytes of tree-selector.ts getSearchableText without
// a label: the role, a space and the first 200 UTF-16 units of the text (a string or the text blocks), with a bash command.
func fixtureSearchTextBytes(t *testing.T, fixture string) int64 {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(fixture, "*.jsonl"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("fixture %s: %v %v", fixture, matches, err)
	}
	f, err := os.Open(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var total int64
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 64<<20)
	for sc.Scan() {
		var record struct {
			Type    string `json:"type"`
			Message struct {
				Role    string          `json:"role"`
				Command string          `json:"command"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &record) != nil || record.Type != "message" {
			continue
		}
		var text string
		if json.Unmarshal(record.Message.Content, &text) != nil {
			var blocks []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			_ = json.Unmarshal(record.Message.Content, &blocks)
			for _, block := range blocks {
				if block.Type == "text" {
					text += block.Text
				}
			}
		}
		units := utf16.Encode([]rune(text))
		total += int64(len(record.Message.Role) + 1 + len(string(utf16.Decode(units[:min(200, len(units))]))) + len(record.Message.Command))
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return total
}
