package pigletbuild

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

// completeWorkspaceSums adds the checksums a workspace build needs and
// `go mod tidy` never records to the overlay's go.work.sum.
//
// A fused member's go.sum is written in module mode. The build of a Pig
// checkout runs in workspace mode, where the Go command also needs lines such as
// the zip hash that decides whether a package path is provided by two modules,
// or the go.mod hash of a module only another workspace module's graph reaches.
// Go records them in go.work.sum, which the build overlay makes read-only, so a
// missing line fails the build with "updates to go.sum needed". The package
// listing runs once in scratch with the same overlay and a writable copy of the
// workspace file, and only the lines it adds join the overlay's go.work.sum.
// Existing lines are never removed.
//
// pig additive (D31): a member needs no checksum its own go.sum cannot derive.
func completeWorkspaceSums(ctx context.Context, source pigSource, goToolchain toolchain.GoToolchain, overlayPath, scratch string, tags []string) error {
	if source.ModuleVersion != "" {
		return nil // a module-cache source builds with GOWORK=off
	}
	workPath := filepath.Join(source.Root, "go.work")
	workData, err := os.ReadFile(workPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read go.work: %w", err)
	}
	backing, err := overlayBacking(overlayPath, filepath.Join(source.Root, "go.work.sum"))
	if err != nil {
		return err
	}
	if backing == "" {
		return nil
	}
	sums, err := os.ReadFile(backing)
	if err != nil {
		return fmt.Errorf("read overlay go.work.sum: %w", err)
	}

	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return err
	}
	scratchWork, err := scratchWorkFile(workPath, workData, source.Root)
	if err != nil {
		return err
	}
	scratchWorkPath := filepath.Join(scratch, "go.work")
	if err := os.WriteFile(scratchWorkPath, scratchWork, 0o644); err != nil {
		return err
	}
	scratchSumPath := filepath.Join(scratch, "go.work.sum")
	if err := os.WriteFile(scratchSumPath, sums, 0o644); err != nil {
		return err
	}

	args := []string{"list", "-deps", "-f", "{{.ImportPath}}", "-overlay", overlayPath}
	if len(tags) > 0 {
		args = append(args, "-tags", strings.Join(tags, ","))
	}
	cmd := source.goCommand(ctx, goToolchain, append(args, "./cmd/pig")...)
	cmd.Env = append(cmd.Env, "GOWORK="+scratchWorkPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("complete workspace checksums: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	completed, err := os.ReadFile(scratchSumPath)
	if err != nil {
		return fmt.Errorf("read completed go.work.sum: %w", err)
	}
	merged := appendMissingLines(sums, completed)
	if bytes.Equal(merged, sums) {
		return nil
	}
	return os.WriteFile(backing, merged, 0o644)
}

// overlayBacking returns the file the overlay at path substitutes for target, or
// "" when it replaces nothing there.
func overlayBacking(path, target string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read build overlay: %w", err)
	}
	var overlay struct {
		Replace map[string]string
	}
	if err := json.Unmarshal(data, &overlay); err != nil {
		return "", fmt.Errorf("parse build overlay: %w", err)
	}
	return overlay.Replace[target], nil
}

// scratchWorkFile rewrites go.work so it resolves from another directory: every
// workspace module and local replacement becomes an absolute path under root.
func scratchWorkFile(path string, data []byte, root string) ([]byte, error) {
	work, err := modfile.ParseWork(path, data, nil)
	if err != nil {
		return nil, fmt.Errorf("parse go.work: %w", err)
	}
	absolute := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(root, filepath.FromSlash(p))
	}
	var out strings.Builder
	if work.Go != nil {
		fmt.Fprintf(&out, "go %s\n", work.Go.Version)
	}
	if work.Toolchain != nil {
		fmt.Fprintf(&out, "toolchain %s\n", work.Toolchain.Name)
	}
	for _, godebug := range work.Godebug {
		fmt.Fprintf(&out, "godebug %s=%s\n", godebug.Key, godebug.Value)
	}
	for _, use := range work.Use {
		fmt.Fprintf(&out, "use %s\n", strconv.Quote(absolute(use.Path)))
	}
	for _, replace := range work.Replace {
		target := replace.New.Path
		if modfile.IsDirectoryPath(target) {
			target = absolute(target)
		}
		fmt.Fprintf(&out, "replace %s => %s\n", versioned(replace.Old.Path, replace.Old.Version), versioned(strconv.Quote(target), replace.New.Version))
	}
	return []byte(out.String()), nil
}

func versioned(path, version string) string {
	if version == "" {
		return path
	}
	return path + " " + version
}

// appendMissingLines appends the lines of extra that base lacks.
func appendMissingLines(base, extra []byte) []byte {
	seen := map[string]struct{}{}
	//portlint:allow pathseparators each line is trimmed, which removes the \r of a CRLF line
	for line := range strings.SplitSeq(string(base), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			seen[line] = struct{}{}
		}
	}
	out := bytes.Clone(base)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	//portlint:allow pathseparators each line is trimmed, which removes the \r of a CRLF line
	for line := range strings.SplitSeq(string(extra), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		out = append(append(out, line...), '\n')
	}
	return out
}
