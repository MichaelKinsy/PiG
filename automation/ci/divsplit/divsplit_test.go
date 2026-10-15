// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func base() map[string]string {
	return map[string]string{
		"head.md":         "# T\n\nrules\n",
		"active-intro.md": "intro\n",
		"D2.md":           "## D2 two\nbody\n\nSCRUTINIZED:approved\n",
		"D10.md":          "## D10 ten\nbody\n",
		"D9.md":           "- D9 — Retired. Why.\n",
		"D3.md":           "- D3 — Retired earlier. Why.\n",
	}
}

func TestGenerateOrdersRecordsNumericallyAndSeparatesRetired(t *testing.T) {
	got, err := Generate(writeDir(t, base()))
	if err != nil {
		t.Fatal(err)
	}
	want := "# T\n\nrules\n\n## Retired divergences\n\n- D3 — Retired earlier. Why.\n\n- D9 — Retired. Why.\n\n## Active divergences\n\nintro\n" +
		"\n## D2 two\nbody\n\nSCRUTINIZED:approved\n\n## D10 ten\nbody\n"
	if string(got) != want {
		t.Fatalf("generated:\n%s\nwant:\n%s", got, want)
	}
}

func TestGenerateRejectsARecordThatIsNotItsFile(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(map[string]string)
		want   string
	}{
		"heading of another number":       {func(f map[string]string) { f["D2.md"] = "## D3 three\n" }, "starts with the heading of D3"},
		"two records in one file":         {func(f map[string]string) { f["D2.md"] += "\n## D4 four\n" }, "second level-two heading"},
		"retired spanning lines":          {func(f map[string]string) { f["D9.md"] += "more\n" }, "one line"},
		"retired entry of another number": {func(f map[string]string) { f["D9.md"] = "- D4 — Retired. Why.\n" }, "starts with the entry of D4"},
		"stray file":                      {func(f map[string]string) { f["notes.md"] = "x" }, "not head.md"},
		"no heading":                      {func(f map[string]string) { f["D2.md"] = "body\n" }, "must start with"},
		"missing head":                    {func(f map[string]string) { delete(f, "head.md") }, "needs head.md"},
	} {
		t.Run(name, func(t *testing.T) {
			files := base()
			tc.mutate(files)
			if _, err := Generate(writeDir(t, files)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestCheckFailsWhenTheLedgerIsStale(t *testing.T) {
	dir := writeDir(t, base())
	out := filepath.Join(t.TempDir(), "DIVERGENCES.md")
	if err := run([]string{"-dir=" + dir, "-out=" + out}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-check", "-dir=" + dir, "-out=" + out}); err != nil {
		t.Fatalf("fresh ledger fails the check: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "D10.md"), []byte("## D10 ten\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-check", "-dir=" + dir, "-out=" + out}); err == nil || !strings.Contains(err.Error(), "make divergences") {
		t.Fatalf("stale ledger passes the check: %v", err)
	}
}
