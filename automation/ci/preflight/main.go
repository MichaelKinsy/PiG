// SPDX-License-Identifier: MIT

// Command preflight runs the cheap checks a pull request must pass before any heavy CI starts, over every open PR of a
// repository on one runner. What it checks, when, and with which tool is declared in config.yaml; this command is the
// engine that runs it:
//
//	preflight run      fetch each PR's data once, decide the cheap checks, run the scans, record status and comment
//	preflight install  put the pinned scanners of config.yaml into a bin directory
//	preflight content  apply forbidden.yaml and mandatory.yaml to a PR's changed files (a scan config.yaml runs)
//	preflight checks   list the checks of config.yaml
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
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	o := runOptions{token: token()}
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
	handledPath := fs.String("handled", "", "write what the job did with each PR to this file, for the verify job")
	next := fs.String("next", "", "write the PRs the budget deferred to this file, as a JSON list of --prs values of --budget PRs each, for next jobs")
	prs := fs.String("prs", "", "PR numbers, space- or comma-separated; empty runs every open PR")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	for f := range strings.FieldsFuncSeq(*prs, func(r rune) bool { return r == ' ' || r == ',' }) {
		n, err := strconv.Atoi(f)
		if err != nil || n < 1 {
			_, _ = fmt.Fprintf(stderr, "preflight run: %q is not a PR number\n", f)
			return 2
		}
		o.prs = append(o.prs, n)
	}
	for _, p := range []*string{&o.root, &o.bin, &o.work} {
		// The tools run in other directories, so every path they are given is absolute.
		if *p != "" {
			abs, err := filepath.Abs(*p)
			if err != nil {
				_, _ = fmt.Fprintln(stderr, "preflight run:", err)
				return 2
			}
			*p = abs
		}
	}
	switch {
	case o.repo == "" || o.bin == "" || o.work == "":
		_, _ = fmt.Fprintln(stderr, "preflight run: --repo, --bin and --work are required")
		return 2
	case o.token == "":
		_, _ = fmt.Fprintln(stderr, "preflight run: GITHUB_TOKEN or GH_TOKEN is required")
		return 2
	}
	hs, err := runBatch(ctx, cfg, o, stdout)
	// Both written also when the job failed, so the deferred PRs still get their job and the verify job its report.
	if *next != "" {
		if werr := writeNext(*next, chunks(deferredPRs(hs), o.budget)); werr != nil {
			err = errors.Join(err, werr)
		}
		for i := range hs {
			hs[i].PassedOn = hs[i].Result == resultDeferred
		}
	}
	if err != nil && len(hs) == 0 {
		// The job could not start: its report says so, rather than look like a repository without open PRs.
		hs = []handled{{Result: resultError, Error: err.Error()}}
	}
	if *handledPath != "" {
		if werr := writeJSON(*handledPath, append([]handled{}, hs...)); werr != nil {
			err = errors.Join(err, werr)
		}
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "preflight run:", err)
		return 2
	}
	return 0
}

// writeNext writes the deferred groups as one line of compact JSON, which the workflow passes on as a job output
// unchanged: ["1 2","3"]. A group keeps its spaces; nothing may strip them.
func writeNext(path string, groups []string) error {
	raw, err := json.Marshal(append([]string{}, groups...))
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}
