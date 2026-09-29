package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Pi's combined provider can enumerate directories while answering current.getSuggestions. Capture the query on the owner loop, but run that filesystem work only in the deferred half.
func TestCombinedProviderDefersDirectoryQueriesAndCapturesInput(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "entry.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Pi autocomplete.ts:750 requires fd for attachment queries, including deferred ones.
	fd := requireFDForTest(t)
	for _, tc := range []struct {
		line      string
		force     bool
		fdPath    string
		wantItems int
	}{
		{"@", false, fd, 1},
		{"./", true, fd, 1},
		// Pi returns no attachment suggestions without fd; direct paths still work.
		{"@", false, "", 0},
		{"./", true, "", 1},
	} {
		provider := NewCombinedProvider(nil, dir, tc.fdPath)
		lines := []string{tc.line}
		prefix, task, ok := provider.SuggestionTask(lines, 0, len(tc.line), tc.force)
		if !ok || task == nil {
			t.Fatalf("directory query %q has no deferred operation", tc.line)
		}
		lines[0] = "@missing-directory/"
		items, err := task(context.Background())
		if err != nil || len(items) != tc.wantItems {
			t.Fatalf("captured query %q with fd %q: prefix=%q items=%v err=%v, want %d items", tc.line, tc.fdPath, prefix, items, err, tc.wantItems)
		}
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := task(cancelled); err == nil {
			t.Fatal("cancelled filesystem query started")
		}
	}
}
