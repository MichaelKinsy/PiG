// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// gitTimeout bounds every git invocation so a stuck repository cannot hang a gate.
const gitTimeout = 120 * time.Second

type repo struct{ root string }

func (r repo) git(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	//nolint:gosec // G204 (class a): program is the literal git; args are each caller's subcommand and revisions, and resolve appends ^{commit} to a name taken from a commit trailer, so git rev-parse --verify cannot read it as an option.
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", r.root}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// gitLines runs git and passes its standard output to fn one line at a time, without the newline, as strings.SplitSeq(out, "\n") would split what git returns (a final line the output does not end in a newline is passed too, and a trailing newline yields no empty last line). Unlike git it never holds the output: a command that prints every revision of a file with its whole text as context, as introductions does, is hundreds of megabytes, and buffering it, then copying it to a string, made the gate peak above 3 GB.
func (r repo) gitLines(args []string, fn func(line string)) error {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	//nolint:gosec // G204 (class a): program is the literal git; args are each caller's subcommand and revisions.
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", r.root}, args...)...)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	in := bufio.NewReaderSize(pipe, 1<<16)
	var readErr error
	for {
		line, err := in.ReadString('\n')
		if line != "" {
			fn(strings.TrimSuffix(line, "\n"))
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				readErr = err
			}
			break
		}
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	if readErr != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), readErr)
	}
	return nil
}

func (r repo) resolve(rev string) (string, bool) {
	out, err := r.git("rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(out), true
}

// releaseBaseline returns the first commit that added the changelog heading of release version. That commit is where the release shipped, so the ledger covers everything after it.
func (r repo) releaseBaseline(version string) (string, error) {
	out, err := r.git("log", "--reverse", "--format=%H", "-S## ["+version+"]", "--", "CHANGELOG.md")
	if err != nil {
		return "", err
	}
	first, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	if first == "" {
		return "", fmt.Errorf("no commit adds the changelog heading for %s; the ledger needs the full history (fetch-depth: 0)", version)
	}
	return first, nil
}

type commitInfo struct {
	Hash    string
	Date    string
	Subject string
	Body    string
}

func (r repo) commit(rev string) (commitInfo, error) {
	out, err := r.git("show", "-s", "--format=%H%x00%ad%x00%s%x00%b", "--date=short", rev)
	if err != nil {
		return commitInfo{}, err
	}
	parts := strings.SplitN(out, "\x00", 4)
	if len(parts) != 4 {
		return commitInfo{}, fmt.Errorf("unexpected git show output for %s", rev)
	}
	return commitInfo{Hash: parts[0], Date: parts[1], Subject: parts[2], Body: strings.TrimSpace(parts[3])}, nil
}
