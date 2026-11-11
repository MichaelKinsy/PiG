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
		_, _ = fmt.Fprintln(stderr, "usage: preflight run|install|checks [flags]")
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
	fs.StringVar(&o.states, "states", "", "directory of the PR states")
	fs.StringVar(&o.work, "work", "", "work directory")
	fs.StringVar(&o.apiBase, "api", "https://api.github.com", "GitHub API base URL")
	fs.StringVar(&o.gitBase, "git", "https://github.com", "where the PRs' heads are fetched from")
	fs.IntVar(&o.jobs, "jobs", 4, "PRs checked at once")
	fs.BoolVar(&o.dryRun, "dry-run", false, "set no status and post no comment")
	fs.BoolVar(&o.forceScans, "force-scans", false, "run the scans even when a cheap check failed")
	fs.IntVar(&o.budget, "budget", 20, "PRs fully checked per run, the longest waiting first; reused PRs do not count; 0 is no limit")
	fs.DurationVar(&o.timeBudget, "time-budget", 0, "start no PR after this long; 0 is no limit")
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
	for _, p := range []*string{&o.root, &o.bin, &o.states, &o.work} {
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
	case o.repo == "" || o.bin == "" || o.states == "" || o.work == "":
		_, _ = fmt.Fprintln(stderr, "preflight run: --repo, --bin, --states and --work are required")
		return 2
	case o.token == "":
		_, _ = fmt.Fprintln(stderr, "preflight run: GITHUB_TOKEN or GH_TOKEN is required")
		return 2
	}
	if err := runBatch(ctx, cfg, o, stdout); err != nil {
		_, _ = fmt.Fprintln(stderr, "preflight run:", err)
		return 2
	}
	return 0
}
