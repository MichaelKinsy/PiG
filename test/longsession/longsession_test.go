// SPDX-License-Identifier: MIT

package longsession

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
)

// The fixture's token count is what PiG's port of Pi's estimateTokens (compaction.ts:298) counts over the context the session file loads to, so a "1M-token" fixture holds a 1M-token context and not a 1M-token file.
func TestFixtureContextHoldsItsTokenCount(t *testing.T) {
	for _, tokens := range []int{1, 50_000, 300_000} {
		dir := t.TempDir()
		meta, err := Write(dir, Options{Tokens: tokens, Window: 1_000_000, ResultBytes: 8192, Seed: 7})
		if err != nil {
			t.Fatal(err)
		}
		if meta.Tokens < tokens {
			t.Fatalf("tokens %d: fixture holds %d", tokens, meta.Tokens)
		}
		session, err := codingagent.NewSessionManagerWithDir("/longsession", dir).Load(meta.Session)
		if err != nil {
			t.Fatal(err)
		}
		messages := session.BuildContext(nil)
		if len(messages) != meta.Entries || meta.Entries != 4*meta.Turns {
			t.Fatalf("tokens %d: %d context messages, %d entries, %d turns", tokens, len(messages), meta.Entries, meta.Turns)
		}
		estimated := 0
		for _, message := range messages {
			estimated += compaction.EstimateTokens(message)
		}
		if estimated != meta.Tokens {
			t.Fatalf("tokens %d: PiG estimates %d, the fixture reports %d", tokens, estimated, meta.Tokens)
		}
		perTurn := meta.Tokens / meta.Turns
		if meta.Tokens-tokens >= perTurn+1 {
			t.Fatalf("tokens %d: overshoot %d is more than one turn (%d)", tokens, meta.Tokens-tokens, perTurn)
		}
		last := messages[len(messages)-1].Assistant
		if last == nil || last.Usage.Input == 0 || last.Usage.Input >= meta.Tokens {
			t.Fatalf("tokens %d: the last message does not report the context before it: %+v", tokens, last)
		}
	}
}

// One size and seed always produce the same bytes, and a larger size with the same seed extends a smaller one turn for turn, so a curve's points share their history.
func TestFixtureIsDeterministic(t *testing.T) {
	var a, b, c bytes.Buffer
	if _, err := Generate(&a, Options{Tokens: 40_000, Window: 1, Seed: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(&b, Options{Tokens: 40_000, Window: 1, Seed: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(&c, Options{Tokens: 80_000, Window: 1, Seed: 3}); err != nil {
		t.Fatal(err)
	}
	other, err := Generate(&bytes.Buffer{}, Options{Tokens: 40_000, Window: 1, Seed: 3, Cwd: "/elsewhere"})
	if err != nil {
		t.Fatal(err)
	}
	if first, _ := Generate(&bytes.Buffer{}, Options{Tokens: 40_000, Window: 1, Seed: 3}); first.ContentSHA256 != other.ContentSHA256 || other.ContentSHA256 == "" {
		t.Fatalf("the content hash depends on the working directory: %q, %q", first.ContentSHA256, other.ContentSHA256)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("the same options wrote different bytes")
	}
	header := bytes.IndexByte(a.Bytes(), '\n')
	if !bytes.HasPrefix(c.Bytes()[bytes.IndexByte(c.Bytes(), '\n'):], a.Bytes()[header:]) {
		t.Fatal("the larger fixture does not extend the smaller one")
	}
}

// A harness appends the prompts it answers to the session it opened, so a measurement opens a staged copy: appending to the copy leaves the fixture as meta.json records it, and a fixture that changed is refused instead of measured as if it were the recorded one.
func TestStageKeepsTheFixtureUnchanged(t *testing.T) {
	dir := t.TempDir()
	meta, err := Write(dir, Options{Tokens: 20_000, Window: 1_000_000, Seed: 5})
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(meta.Session)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := meta.Stage(filepath.Join(dir, "run-1"))
	if err != nil {
		t.Fatal(err)
	}
	if staged == meta.Session {
		t.Fatal("Stage returned the fixture itself")
	}
	appendLine := func(path string) {
		t.Helper()
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(`{"type":"message","id":"x","parentId":"b0","timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"user","content":"appended"}}` + "\n"); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	appendLine(staged)
	if now, err := os.ReadFile(meta.Session); err != nil || !bytes.Equal(now, original) {
		t.Fatalf("appending to the staged copy changed the fixture (err %v)", err)
	}
	if _, err := meta.Stage(filepath.Join(dir, "run-2")); err != nil {
		t.Fatalf("an unchanged fixture was refused: %v", err)
	}
	appendLine(meta.Session)
	if _, err := meta.Stage(filepath.Join(dir, "run-3")); err == nil {
		t.Fatal("a fixture that no longer matches meta.json was staged")
	}
}
