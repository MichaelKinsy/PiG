// External-editor support.
//
// Mirrors upstream openExternalEditor() in
// .upstream/current/packages/coding-agent/src/modes/interactive/interactive-mode.ts:3358.
// User presses Ctrl+G; pig:
//   1. Writes the current editor buffer into a tempfile (suffix .md so
//      the user's editor highlights markdown).
//   2. Restores cooked-mode terminal so the editor can take over the
//      TTY. (Done by caller, not this helper.)
//   3. Resolves the configured command, VISUAL, EDITOR, or the platform default,
//      then spawns it (split on literal spaces; first token is
//      the binary, rest are arguments) with the tempfile path appended,
//      stdio inherited.
//   4. On exit code 0, reads the file back, strips one trailing newline
//      (most editors append one), returns the content.
//   5. On non-zero exit OR read error, returns the initial text plus
//      an error so caller can leave the editor buffer untouched.
//   6. Tempfile always removed.
//
// Re-entering raw mode and triggering a full re-render is the caller's
// responsibility. Single-responsibility split keeps this helper trivial
// to unit-test with a fake editor binary.

package codingagent

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/text"
)

// OpenExternalEditor resolves the command with SettingsManager's precedence, writes initial into a tempfile, runs the editor with inherited stdio, and returns the file contents on
// success. On editor non-zero exit OR read error, returns initial
// unchanged plus a non-nil error.
//
// The tempfile is always removed before this function returns.
//
// Caller is responsible for:
//   - restoring cooked-mode terminal before calling (so the editor's
//     stdin/stdout/stderr inherit a usable TTY);
//   - re-entering raw mode and triggering a full re-render after.
//
// Reference: upstream openExternalEditor() in modes/interactive/interactive-mode.ts.
func OpenExternalEditor(ctx context.Context, initial string, configuredEditor string) (string, error) {
	editorCmd := resolveExternalEditorCommand(configuredEditor, os.Getenv("VISUAL"), os.Getenv("EDITOR"), runtime.GOOS)

	tmp, err := os.CreateTemp("", "pig-editor-*.md")
	if err != nil {
		return initial, fmt.Errorf("external editor: tempfile: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.WriteString(initial); err != nil {
		_ = tmp.Close()
		return initial, fmt.Errorf("external editor: write initial: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return initial, fmt.Errorf("external editor: close tmp: %w", err)
	}

	// Pi splits on literal spaces, preserving empty arguments. Quoting and tab expansion are left to cmd.exe only on Windows.
	parts := strings.Split(editorCmd, " ")
	args := append([]string{}, parts[1:]...)
	args = append(args, tmpPath)

	cmd := editorCommand(ctx, parts[0], args)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	_, _ = fmt.Fprintf(os.Stdout, "Launching external editor: %s\nPi will resume when the editor exits.\n", editorCmd)

	if err := cmd.Run(); err != nil {
		// Editor exited non-zero (or failed to spawn). Match upstream:
		// keep the original text, surface an error so the caller can
		// log/flash. Do NOT read tempfile content: user may have
		// abandoned with :cq specifically to discard.
		return initial, fmt.Errorf("external editor: run %s: %w", parts[0], err)
	}

	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return initial, fmt.Errorf("external editor: read result: %w", err)
	}
	// Strip exactly one trailing newline (most editors append one).
	out := strings.TrimSuffix(text.StripBom(string(data)), "\n")
	return out, nil
}
