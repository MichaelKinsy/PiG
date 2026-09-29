package gitsnapshot

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Create writes an unreachable commit for the current working tree through a
// temporary index. It does not change the repository index or checked-out ref.
func Create(ctx context.Context, root, indexPath string) (string, error) {
	env := append(os.Environ(),
		"GIT_INDEX_FILE="+indexPath,
		"GIT_AUTHOR_NAME=PiG test",
		"GIT_AUTHOR_EMAIL=pig-test@example.invalid",
		"GIT_AUTHOR_DATE=2000-01-01T00:00:00Z",
		"GIT_COMMITTER_NAME=PiG test",
		"GIT_COMMITTER_EMAIL=pig-test@example.invalid",
		"GIT_COMMITTER_DATE=2000-01-01T00:00:00Z",
	)
	run := func(input string, args ...string) (string, error) {
		command := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
		command.Env = env
		if input != "" {
			command.Stdin = strings.NewReader(input)
		}
		// The tree and commit IDs come from stdout. Stderr carries git's
		// warnings and traces, so it only explains a failure.
		var stderr bytes.Buffer
		command.Stderr = &stderr
		output, err := command.Output()
		if err != nil {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(stderr.Bytes()))
		}
		return strings.TrimSpace(string(output)), nil
	}
	if _, err := run("", "read-tree", "--empty"); err != nil {
		return "", err
	}
	if _, err := run("", "add", "-A", "--", "."); err != nil {
		return "", err
	}
	tree, err := run("", "write-tree")
	if err != nil {
		return "", err
	}
	return run("PiG public-root test snapshot\n", "commit-tree", tree)
}
