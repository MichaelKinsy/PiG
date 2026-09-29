package correspondence

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// resolveCommitPath returns the path for a repository-relative file at a
// commit. It tries the current source prefix and each shallower prefix so the
// same correspondence code works in a nested checkout and at the repository
// root.
func resolveCommitPath(ctx context.Context, root, commit, currentPrefix, relativePath string) (string, error) {
	candidates := prefixCandidates(currentPrefix)
	for _, candidate := range candidates {
		object := commit + ":" + candidate + relativePath
		command := exec.CommandContext(ctx, "git", "cat-file", "-e", object)
		command.Dir = root
		if command.Run() == nil {
			return object, nil
		}
	}
	return "", fmt.Errorf("locate %s at %s: not found under any of %v", relativePath, commit, candidates)
}

// prefixCandidates lists the prefixes to try, current layout first, then each
// shallower one produced by dropping a leading component.
func prefixCandidates(currentPrefix string) []string {
	trimmed := strings.Trim(currentPrefix, "/")
	if trimmed == "" {
		return []string{""}
	}
	parts := strings.Split(trimmed, "/")
	candidates := make([]string, 0, len(parts)+1)
	for i := range parts {
		candidates = append(candidates, strings.Join(parts[i:], "/")+"/")
	}
	return append(candidates, "")
}
