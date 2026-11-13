// SPDX-License-Identifier: MIT

// Command preflight is the PiG CI pre-flight; see README.md.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	cfg, err := loadConfig(configYAML)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "preflight:", err)
		return 2
	}
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: preflight run|install|checks|content|verify [flags]")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch args[0] {
	case "run":
		return runCommand(ctx, cfg, args[1:], stdout, stderr)
	case "install":
		return installCommand(ctx, cfg, args[1:], stdout, stderr)
	case "content":
		return runContent(args[1:], stdout, stderr)
	case "verify":
		return runVerify(ctx, args[1:], stdout, stderr)
	case "checks":
		for _, c := range cfg.Checks {
			_, _ = fmt.Fprintf(stdout, "%-20s %s\n", c.Name, c.Stage)
		}
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "preflight: unknown command %q\n", args[0])
		return 2
	}
}

func installCommand(ctx context.Context, cfg config, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "main's tree")
	bin := fs.String("bin", "", "directory for the scanners")
	cache := fs.String("cache", defaultCache(), "digest-keyed download cache")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *bin == "" {
		_, _ = fmt.Fprintln(stderr, "preflight install: --bin is required")
		return 2
	}
	if err := install(ctx, cfg, *root, *bin, *cache, stdout); err != nil {
		_, _ = fmt.Fprintln(stderr, "preflight install:", err)
		return 1
	}
	return 0
}

func defaultCache() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "pig-ci-downloads")
	}
	return filepath.Join(dir, "pig-ci-downloads")
}

func runCommand(ctx context.Context, cfg config, args []string, stdout, stderr io.Writer) int {
	o, reports, err := parseRunFlags(args, stderr)
	if err != nil {
		if !errors.Is(err, errUsage) {
			_, _ = fmt.Fprintln(stderr, "preflight run:", err)
		}
		return 2
	}
	hs, err := runBatch(ctx, cfg, o, stdout)
	if err = errors.Join(err, reports.write(hs, err, o.budget)); err != nil {
		_, _ = fmt.Fprintln(stderr, "preflight run:", err)
		return 2
	}
	return 0
}

type runReports struct{ next, handled string }

// errUsage is a flag error the flag package has already printed.
var errUsage = errors.New("usage")

func parseRunFlags(args []string, stderr io.Writer) (runOptions, runReports, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	o := runOptions{token: token()}
	var reports runReports
	fs.StringVar(&o.repo, "repo", os.Getenv("GITHUB_REPOSITORY"), "owner/repo whose PRs to check")
	fs.StringVar(&o.root, "root", ".", "main's tree")
	fs.StringVar(&o.bin, "bin", "", "main's built tools and the scanners")
	fs.StringVar(&o.work, "work", "", "work directory")
	fs.StringVar(&o.apiBase, "api", "https://api.github.com", "GitHub API base URL")
	fs.StringVar(&o.gitBase, "git", "https://github.com", "where the PRs' heads are fetched from")
	fs.IntVar(&o.jobs, "jobs", 4, "PRs checked at once")
	fs.BoolVar(&o.dryRun, "dry-run", false, "set no status and post no comment")
	fs.BoolVar(&o.forceScans, "force-scans", false, "run the scans even when a cheap check failed")
	fs.IntVar(&o.budget, "budget", 200, "PRs a job fully checks before it leaves the rest to next jobs (see --next); reused PRs do not count; 0 is no limit")
	fs.DurationVar(&o.timeBudget, "time-budget", 0, "start no PR after this long; 0 is no limit")
	fs.StringVar(&o.bot, "bot", defaultBot, "the account the pre-flight comments as; only its comment holds a PR's state")
	fs.StringVar(&reports.handled, "handled", "", "write what the job did with each PR to this file, for the verify job")
	fs.StringVar(&reports.next, "next", "", "write the PRs the budget deferred to this file, as a JSON list of --prs values of --budget PRs each, for next jobs")
	prs := fs.String("prs", "", "PR numbers, space- or comma-separated; empty runs every open PR")
	if err := fs.Parse(args); err != nil {
		return o, reports, errUsage
	}
	var err error
	if o.prs, err = parsePRs(*prs); err != nil {
		return o, reports, err
	}
	// The tools run in other directories.
	for _, p := range []*string{&o.root, &o.bin, &o.work} {
		if *p != "" {
			if *p, err = filepath.Abs(*p); err != nil {
				return o, reports, err
			}
		}
	}
	switch {
	case o.repo == "" || o.bin == "" || o.work == "":
		return o, reports, errors.New("--repo, --bin and --work are required")
	case o.token == "":
		return o, reports, errors.New("GITHUB_TOKEN or GH_TOKEN is required")
	}
	return o, reports, nil
}

func parsePRs(list string) ([]int, error) {
	var prs []int
	for f := range strings.FieldsFuncSeq(list, func(r rune) bool { return r == ' ' || r == ',' }) {
		n, err := strconv.Atoi(f)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("%q is not a PR number", f)
		}
		prs = append(prs, n)
	}
	return prs, nil
}

// Also when the job failed: the deferred PRs still need their jobs, and verify its report.
func (r runReports) write(hs []handled, runErr error, budget int) error {
	var err error
	if r.next != "" {
		err = writeNext(r.next, chunks(deferredPRs(hs), budget))
		for i := range hs {
			hs[i].PassedOn = hs[i].Result == resultDeferred
		}
	}
	if runErr != nil && len(hs) == 0 {
		hs = []handled{{Result: resultError, Error: runErr.Error()}}
	}
	if r.handled != "" {
		err = errors.Join(err, writeJSON(r.handled, append([]handled{}, hs...)))
	}
	return err
}

// One line, with the spaces inside each group: the workflow passes it on as a job output unchanged.
func writeNext(path string, groups []string) error {
	raw, err := json.Marshal(append([]string{}, groups...))
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}
