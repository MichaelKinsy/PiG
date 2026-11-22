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
	"strconv"
	"strings"
	"time"
)

// gitTimeout is a hang guard for git invocations: on this history, git log -U100000 and git rev-list take seconds on a warm cache, so 15 minutes only fires on a stuck process or consumer.
const gitTimeout = 15 * time.Minute

type repo struct{ root string }

func (r repo) git(args ...string) (string, error) {
	return r.gitWithTimeout(gitTimeout, args...)
}

func (r repo) gitWithTimeout(timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	//nolint:gosec // G204 (class a): program is the literal git; args are each caller's subcommand and revisions, and resolve appends ^{commit} to a name taken from a commit trailer, so git rev-parse --verify cannot read it as an option.
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", r.root}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", gitCmdError(ctx, args, timeout, err, errb.String())
	}
	return out.String(), nil
}

// gitLines runs git and passes its standard output to fn one line at a time, without the newline, as strings.SplitSeq(out, "\n") would split what git returns (a final line the output does not end in a newline is passed too, and a trailing newline yields no empty last line). Unlike git it never holds the output: a command that prints every revision of a file with its whole text as context, as introductions does, is hundreds of megabytes, and buffering it, then copying it to a string, made the gate peak above 3 GB.
func (r repo) gitLines(args []string, fn func(line string)) error {
	return r.gitLinesWithTimeout(gitTimeout, args, fn)
}

func (r repo) gitLinesWithTimeout(timeout time.Duration, args []string, fn func(line string)) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
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
		return gitCmdError(ctx, args, timeout, err, errb.String())
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
		return gitCmdError(ctx, args, timeout, err, errb.String())
	}
	if readErr != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), readErr)
	}
	return nil
}

func gitCmdError(ctx context.Context, args []string, timeout time.Duration, err error, stderr string) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		subcmd := ""
		if len(args) > 0 {
			subcmd = " " + args[0]
		}
		return fmt.Errorf("git%s did not finish in %s: the host is slow or loaded, or the reader of its output is slow: %w", subcmd, timeout, ctx.Err())
	}
	return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr))
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
	Hash string
	// Date is the author date in the author's own time zone, as the ledger shows it; At is the same moment as an instant.
	Date    string
	At      time.Time
	Subject string
	Body    string
}

func (r repo) commit(rev string) (commitInfo, error) {
	out, err := r.git("show", "-s", "--format=%H%x00%ad%x00%at%x00%s%x00%b", "--date=short", rev)
	if err != nil {
		return commitInfo{}, err
	}
	parts := strings.SplitN(out, "\x00", 5)
	if len(parts) != 5 {
		return commitInfo{}, fmt.Errorf("unexpected git show output for %s", rev)
	}
	secs, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return commitInfo{}, fmt.Errorf("git show %s: author time %q: %w", rev, parts[2], err)
	}
	return commitInfo{Hash: parts[0], Date: parts[1], At: time.Unix(secs, 0).UTC(), Subject: parts[3], Body: strings.TrimSpace(parts[4])}, nil
}

// authorInstants returns the author instant of each of the commits the abbreviated or full hashes name, keyed by full hash, with one git call. A hash the repository does not hold is left out.
func (r repo) authorInstants(hashes []string) (map[string]time.Time, error) {
	if len(hashes) == 0 {
		return nil, nil
	}
	args := append([]string{"log", "--no-walk=unsorted", "--ignore-missing", "--format=%H %at"}, hashes...)
	out, err := r.git(append(args, "--")...)
	if err != nil {
		return nil, err
	}
	found := map[string]time.Time{}
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		hash, secs, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(secs, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("git log: author time %q of %s: %w", secs, hash, err)
		}
		found[hash] = time.Unix(n, 0).UTC()
	}
	return found, nil
}
