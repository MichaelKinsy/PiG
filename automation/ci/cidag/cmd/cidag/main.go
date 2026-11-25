// SPDX-License-Identifier: MIT

// Command cidag computes fail-closed affected-package and affected-lane CI plans
// for PiG, replays commit history for empirical benchmarking, and audits shadow
// plans against failing CI lanes to detect dependency-graph blind spots.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/automation/ci/cidag"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "cidag:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("cidag", flag.ContinueOnError)
	root := fs.String("root", ".", "repository root")
	base := fs.String("base", "", "base commit/ref for git diff (<merge-base>...<head>)")
	head := fs.String("head", "HEAD", "head commit/ref for git diff")
	filesFrom := fs.String("files-from", "", "read changed repository-relative file paths from file (one per line; '-' for stdin)")
	filesCSV := fs.String("files", "", "comma-separated changed repository-relative file paths")
	maxFiles := fs.Int("max-files", cidag.DefaultMaxFiles, "fail-closed ceiling on changed file count")
	modeStr := fs.String("mode", string(cidag.ModeOn), "selector mode: off, shadow, on")
	event := fs.String("event", "pull_request", "GitHub Actions event name")
	lightMode := fs.String("light-mode", "", "PIG_PR_CI_LIGHT value: empty, queue or label")
	fullRequested := fs.Bool("full-requested", false, "treat as full run requested (ci:full label, ready_for_review, or os-specific)")
	tiersFile := fs.String("tiers-file", "", "path to automation/ci/ci-tiers.txt (with -github-output)")
	githubOutput := fs.Bool("github-output", false, "emit ci-plan.sh GITHUB_OUTPUT key=value lines")
	planOut := fs.String("plan-out", "", "write JSON plan artifact to this file (ci-plan.json)")
	summaryFile := fs.String("summary", "", "append Markdown plan summary to this file ($GITHUB_STEP_SUMMARY)")
	jsonOut := fs.Bool("json", false, "print JSON output to stdout")
	shadow := fs.Bool("shadow", false, "shorthand for -mode=shadow")
	auditRun := fs.Bool("audit-run", false, "audit computed plan against -failed-lanes (exit non-zero on blind spots)")
	failedLanes := fs.String("failed-lanes", "", "comma-separated lanes that failed in a full CI run (with -audit-run)")
	auditTier := fs.String("audit-tier", "full", "tier to audit against: full (plan.lanes) or pr (plan.pr_lanes)")
	runID := fs.String("run-id", "", "CI run identifier recorded in audit reports")
	replayN := fs.Int("replay", 0, "replay the last N non-merge commits from git log and print timing + lane reduction stats")
	replayIncludeMerges := fs.Bool("replay-merges", false, "include merge commits in -replay")

	if err := fs.Parse(args); err != nil {
		return err
	}

	mode, err := cidag.ParseMode(*modeStr)
	if err != nil {
		return err
	}
	if *shadow {
		mode = cidag.ModeShadow
	}

	absRoot, err := filepath.Abs(*root)
	if err != nil {
		return fmt.Errorf("resolve root: %w", err)
	}

	ctx := context.Background()

	if *replayN > 0 {
		return runReplay(ctx, absRoot, *replayN, *replayIncludeMerges, *maxFiles, *jsonOut)
	}

	files, baseRef, headRef, err := resolveChangedFiles(ctx, absRoot, *filesFrom, *filesCSV, *base, *head)
	if err != nil {
		return err
	}

	forceFull := *event != "pull_request" || *fullRequested
	forceReason := ""
	if *event != "pull_request" {
		forceReason = "event:" + *event
	} else if *fullRequested {
		forceReason = "full-requested"
	}

	var g *cidag.Graph
	// Load the cross-GOOS package graph only when files cannot be resolved purely
	// by force-full / docs-only / max-files short-circuits.
	needGraph := !forceFull && len(files) > 0 && len(files) <= *maxFiles && needsGoGraph(files, cidag.DefaultRules)
	if needGraph {
		g, err = cidag.LoadGraph(ctx, absRoot, cidag.DefaultGOOSList, cidag.DefaultBuildTags)
		if err != nil {
			// Fail closed: tool error -> full plan with error reason recorded.
			forceFull = true
			forceReason = "graph-error:" + err.Error()
		}
	} else if !forceFull && len(files) > 0 && len(files) <= *maxFiles {
		// Provide an empty initialized graph so pure docs-only / RuleFull sets do not
		// trigger the nil-graph fail-closed guard.
		g = &cidag.Graph{Root: absRoot, Module: cidag.DefaultModule}
	}

	plan := cidag.Select(files, g, cidag.DefaultRules, cidag.Options{
		MaxFiles:        *maxFiles,
		Mode:            mode,
		Event:           *event,
		Base:            baseRef,
		Head:            headRef,
		ForceFull:       forceFull,
		ForceFullReason: forceReason,
	})

	if *planOut != "" {
		if err := writeJSONFile(*planOut, plan); err != nil {
			return err
		}
	}
	if *summaryFile != "" {
		if err := appendSummary(*summaryFile, plan); err != nil {
			return err
		}
	}

	if *auditRun {
		var failing []string
		for part := range strings.SplitSeq(*failedLanes, ",") {
			if p := strings.TrimSpace(part); p != "" {
				failing = append(failing, p)
			}
		}
		report := cidag.AuditRun(plan, failing, *auditTier, *runID, headRef)
		if *jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(report); err != nil {
				return err
			}
		} else {
			fmt.Printf("audit ok=%t tier=%s plan_full=%t covered=%d blind_spots=%d", report.OK, report.Tier, report.PlanFull, len(report.CoveredLanes), len(report.BlindSpots))
			if len(report.BlindSpots) > 0 {
				fmt.Printf(" missed=[%s]", strings.Join(report.BlindSpots, ","))
			}
			fmt.Println()
		}
		if !report.OK {
			return fmt.Errorf("shadow audit found %d blind spot(s): %s", len(report.BlindSpots), strings.Join(report.BlindSpots, ", "))
		}
		return nil
	}

	if *githubOutput {
		tablePath := *tiersFile
		if tablePath == "" {
			tablePath = filepath.Join(absRoot, "automation", "ci", "ci-tiers.txt")
		}
		rows, err := cidag.LoadTierTable(tablePath)
		if err != nil {
			return err
		}
		var activePlan *cidag.Plan
		if mode == cidag.ModeOn {
			activePlan = &plan
		}
		rendered, err := cidag.RenderCIPlan(rows, *event, *lightMode, *fullRequested, activePlan)
		if err != nil {
			return err
		}
		out, err := rendered.FormatGitHubOutput()
		if err != nil {
			return err
		}
		fmt.Print(out)
		return nil
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(plan)
	}

	fmt.Printf("mode=%s full=%t docs_only=%t files=%d lanes=%d/%d (%.1f/%.1f min) pr_lanes=%d (%.1f min) affected_pkgs=%d scenarios=%d/%d/%d\n",
		plan.Mode, plan.Full, plan.DocsOnly, plan.FileCount,
		len(plan.Lanes), len(cidag.AllLanes()), plan.Minutes, plan.FullMinutes,
		len(plan.PRLanes), plan.PRMinutes,
		len(plan.AffectedPackages),
		len(plan.ScenariosClosure), len(plan.ScenariosDir), len(plan.ScenariosDirect))
	return nil
}

// needsGoGraph reports whether the file set requires invoking `go list`.
// Pure docs-only changes and changes already containing a RuleFull file can
// resolve in milliseconds without paying the 3-GOOS `go list` cost.
func needsGoGraph(files []string, rules []cidag.PathRule) bool {
	allDocs := true
	for _, f := range files {
		r, ok := cidag.MatchRule(f, rules)
		if !ok {
			return true
		}
		if r.Kind == cidag.RuleFull {
			return false
		}
		if r.Kind != cidag.RuleDocs {
			allDocs = false
		}
	}
	return !allDocs
}

func resolveChangedFiles(ctx context.Context, absRoot, filesFrom, filesCSV, base, head string) (files []string, baseRef, headRef string, err error) {
	if filesFrom != "" {
		var data []byte
		if filesFrom == "-" {
			data, err = os.ReadFile("/dev/stdin")
		} else {
			data, err = os.ReadFile(filesFrom)
		}
		if err != nil {
			return nil, "", "", fmt.Errorf("read files-from: %w", err)
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				files = append(files, trimmed)
			}
		}
		return files, base, head, nil
	}
	if filesCSV != "" {
		for part := range strings.SplitSeq(filesCSV, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				files = append(files, trimmed)
			}
		}
		return files, base, head, nil
	}
	if base == "" {
		return nil, "", "", errors.New("specify -base, -files-from, -files, or -replay")
	}
	// Resolve merge-base for pull_request diffs; fallback to direct range if base is already an ancestor.
	mbOut, err := gitOutput(ctx, absRoot, "merge-base", head, base)
	diffRange := base + "..." + head
	if err == nil {
		if mb := strings.TrimSpace(mbOut); mb != "" {
			diffRange = mb + "..." + head
		}
	}
	out, err := gitOutput(ctx, absRoot, "diff", "--name-only", "--no-renames", diffRange)
	if err != nil {
		return nil, "", "", fmt.Errorf("git diff %s: %w", diffRange, err)
	}
	for line := range strings.SplitSeq(out, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			files = append(files, trimmed)
		}
	}
	return files, base, head, nil
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	//nolint:gosec // git binary with explicit subcommands in repository root.
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}

type replayCommitRow struct {
	SHA              string   `json:"sha"`
	Subject          string   `json:"subject"`
	Files            int      `json:"files"`
	Full             bool     `json:"full"`
	DocsOnly         bool     `json:"docs_only"`
	Lanes            int      `json:"lanes"`
	PRLanes          int      `json:"pr_lanes"`
	TotalLanes       int      `json:"total_lanes"`
	Minutes          float64  `json:"minutes"`
	PRMinutes        float64  `json:"pr_minutes"`
	FullMinutes      float64  `json:"full_minutes"`
	CompilePkgs      int      `json:"compile_pkgs"`
	AffectedPkgs     int      `json:"affected_pkgs"`
	ScenariosClosure int      `json:"scenarios_closure"`
	ScenariosDir     int      `json:"scenarios_dir"`
	ScenariosDirect  int      `json:"scenarios_direct"`
	Unowned          int      `json:"unowned"`
	SelectMS         float64  `json:"select_ms"`
	FullReason       []string `json:"full_reason,omitempty"`
}

type replaySummary struct {
	Commits              int               `json:"commits"`
	GraphLoadSeconds     float64           `json:"graph_load_seconds"`
	TotalSelectSeconds   float64           `json:"total_select_seconds"`
	MeanSelectMS         float64           `json:"mean_select_ms"`
	MedianSelectMS       float64           `json:"median_select_ms"`
	TotalLanes           int               `json:"total_lanes"`
	FullMinutes          float64           `json:"full_minutes"`
	FullCommits          int               `json:"full_commits"`
	DocsOnlyCommits      int               `json:"docs_only_commits"`
	MeanLanes            float64           `json:"mean_lanes"`
	MedianLanes          float64           `json:"median_lanes"`
	MeanPRLanes          float64           `json:"mean_pr_lanes"`
	MedianPRLanes        float64           `json:"median_pr_lanes"`
	MeanMinutes          float64           `json:"mean_minutes"`
	MedianMinutes        float64           `json:"median_minutes"`
	MeanPRMinutes        float64           `json:"mean_pr_minutes"`
	MedianPRMinutes      float64           `json:"median_pr_minutes"`
	LaneReductionPct     float64           `json:"lane_reduction_pct"`
	PRLaneReductionPct   float64           `json:"pr_lane_reduction_pct"`
	MinuteReductionPct   float64           `json:"minute_reduction_pct"`
	PRMinuteReductionPct float64           `json:"pr_minute_reduction_pct"`
	Rows                 []replayCommitRow `json:"rows"`
}

func runReplay(ctx context.Context, absRoot string, n int, includeMerges bool, maxFiles int, jsonOut bool) error {
	logArgs := []string{"log", fmt.Sprintf("-n%d", n), "--format=%H\t%s"}
	if !includeMerges {
		logArgs = append(logArgs, "--no-merges")
	}
	logOut, err := gitOutput(ctx, absRoot, logArgs...)
	if err != nil {
		return fmt.Errorf("git log: %w", err)
	}
	type commitEntry struct {
		sha     string
		subject string
		files   []string
	}
	var commits []commitEntry
	for line := range strings.SplitSeq(strings.TrimSpace(logOut), "\n") {
		sha, subj, ok := strings.Cut(line, "\t")
		if !ok || sha == "" {
			continue
		}
		diffOut, err := gitOutput(ctx, absRoot, "diff-tree", "--no-commit-id", "--name-only", "--no-renames", "-r", "-m", "--first-parent", sha)
		if err != nil {
			return fmt.Errorf("git diff-tree %s: %w", sha[:9], err)
		}
		var files []string
		for f := range strings.SplitSeq(diffOut, "\n") {
			if trimmed := strings.TrimSpace(f); trimmed != "" {
				files = append(files, trimmed)
			}
		}
		commits = append(commits, commitEntry{sha: sha, subject: subj, files: files})
	}
	if len(commits) == 0 {
		return errors.New("replay: no commits found")
	}

	tGraph := time.Now()
	g, err := cidag.LoadGraph(ctx, absRoot, cidag.DefaultGOOSList, cidag.DefaultBuildTags)
	if err != nil {
		return err
	}
	graphLoadSec := time.Since(tGraph).Seconds()

	totalLanes := len(cidag.AllLanes())
	fullMin := math.Round(cidag.FullMinutes()*10) / 10

	rows := make([]replayCommitRow, 0, len(commits))
	var selectMS, laneCounts, prLaneCounts, mins, prMins []float64
	fullCommits, docsOnly := 0, 0

	tSelStart := time.Now()
	for _, c := range commits {
		p := cidag.Select(c.files, g, cidag.DefaultRules, cidag.Options{
			MaxFiles: maxFiles,
			Mode:     cidag.ModeShadow,
			Event:    "pull_request",
			Head:     c.sha,
		})
		if p.Full {
			fullCommits++
		}
		if p.DocsOnly {
			docsOnly++
		}
		rows = append(rows, replayCommitRow{
			SHA:              c.sha[:9],
			Subject:          c.subject,
			Files:            p.FileCount,
			Full:             p.Full,
			DocsOnly:         p.DocsOnly,
			Lanes:            len(p.Lanes),
			PRLanes:          len(p.PRLanes),
			TotalLanes:       totalLanes,
			Minutes:          p.Minutes,
			PRMinutes:        p.PRMinutes,
			FullMinutes:      p.FullMinutes,
			CompilePkgs:      len(p.CompilePackages),
			AffectedPkgs:     len(p.AffectedPackages),
			ScenariosClosure: len(p.ScenariosClosure),
			ScenariosDir:     len(p.ScenariosDir),
			ScenariosDirect:  len(p.ScenariosDirect),
			Unowned:          len(p.Unowned),
			SelectMS:         p.SelectDurationMS,
			FullReason:       p.FullReason,
		})
		selectMS = append(selectMS, p.SelectDurationMS)
		laneCounts = append(laneCounts, float64(len(p.Lanes)))
		prLaneCounts = append(prLaneCounts, float64(len(p.PRLanes)))
		mins = append(mins, p.Minutes)
		prMins = append(prMins, p.PRMinutes)
	}
	totalSelectSec := time.Since(tSelStart).Seconds()

	meanLanes := mean(laneCounts)
	meanPRLanes := mean(prLaneCounts)
	meanMins := mean(mins)
	meanPRMins := mean(prMins)

	summary := replaySummary{
		Commits:              len(rows),
		GraphLoadSeconds:     round2(graphLoadSec),
		TotalSelectSeconds:   round3(totalSelectSec),
		MeanSelectMS:         round2(mean(selectMS)),
		MedianSelectMS:       round2(median(selectMS)),
		TotalLanes:           totalLanes,
		FullMinutes:          fullMin,
		FullCommits:          fullCommits,
		DocsOnlyCommits:      docsOnly,
		MeanLanes:            round2(meanLanes),
		MedianLanes:          round2(median(laneCounts)),
		MeanPRLanes:          round2(meanPRLanes),
		MedianPRLanes:        round2(median(prLaneCounts)),
		MeanMinutes:          round1(meanMins),
		MedianMinutes:        round1(median(mins)),
		MeanPRMinutes:        round1(meanPRMins),
		MedianPRMinutes:      round1(median(prMins)),
		LaneReductionPct:     round1((1 - meanLanes/float64(totalLanes)) * 100),
		PRLaneReductionPct:   round1((1 - meanPRLanes/float64(totalLanes)) * 100),
		MinuteReductionPct:   round1((1 - meanMins/fullMin) * 100),
		PRMinuteReductionPct: round1((1 - meanPRMins/fullMin) * 100),
		Rows:                 rows,
	}

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(summary)
	}

	fmt.Printf("Replay of %d commits (graph load: %.2fs across 3 GOOS; total select: %.3fs, mean %.2fms, median %.2fms)\n",
		summary.Commits, summary.GraphLoadSeconds, summary.TotalSelectSeconds, summary.MeanSelectMS, summary.MedianSelectMS)
	fmt.Printf("Catalogue: %d lanes (%.1f full runner-min) | full-plan commits: %d/%d | docs-only: %d\n",
		summary.TotalLanes, summary.FullMinutes, summary.FullCommits, summary.Commits, summary.DocsOnlyCommits)
	fmt.Printf("Affected (full tier): mean %.2f/%d lanes (-%.1f%%), median %.1f | mean %.1f/%.1f min (-%.1f%%), median %.1f\n",
		summary.MeanLanes, summary.TotalLanes, summary.LaneReductionPct, summary.MedianLanes,
		summary.MeanMinutes, summary.FullMinutes, summary.MinuteReductionPct, summary.MedianMinutes)
	fmt.Printf("Tiered PR push:       mean %.2f/%d lanes (-%.1f%%), median %.1f | mean %.1f/%.1f min (-%.1f%%), median %.1f\n\n",
		summary.MeanPRLanes, summary.TotalLanes, summary.PRLaneReductionPct, summary.MedianPRLanes,
		summary.MeanPRMinutes, summary.FullMinutes, summary.PRMinuteReductionPct, summary.MedianPRMinutes)

	for _, r := range rows {
		reason := ""
		if len(r.FullReason) > 0 {
			reason = " [" + strings.Join(r.FullReason, ", ") + "]"
		}
		fmt.Printf("%s files=%3d full=%-5t lanes=%2d/%2d pr=%2d min=%5.1f pr_min=%5.1f comp=%2d aff=%3d scen=%4d/%3d/%2d sel=%6.2fms%s  %s\n",
			r.SHA, r.Files, r.Full, r.Lanes, r.TotalLanes, r.PRLanes, r.Minutes, r.PRMinutes,
			r.CompilePkgs, r.AffectedPkgs, r.ScenariosClosure, r.ScenariosDir, r.ScenariosDirect, r.SelectMS, reason, truncate(r.Subject, 64))
	}
	return nil
}

func writeJSONFile(path string, v any) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func appendSummary(path string, plan cidag.Plan) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	var b strings.Builder
	fmt.Fprintf(&b, "### CI DAG Plan (`%s`, mode=`%s`)\n\n", plan.Selector, plan.Mode)
	fmt.Fprintf(&b, "- **Full plan:** `%t` (docs-only: `%t`, changed files: `%d`)\n", plan.Full, plan.DocsOnly, plan.FileCount)
	fmt.Fprintf(&b, "- **Affected lanes:** `%d/%d` (%.1f / %.1f runner-min); **PR tier:** `%d` (%.1f runner-min)\n",
		len(plan.Lanes), len(cidag.AllLanes()), plan.Minutes, plan.FullMinutes, len(plan.PRLanes), plan.PRMinutes)
	fmt.Fprintf(&b, "- **Affected Go packages:** `%d` (compile: `%d`, test-only: `%d`, guards: `%d`)\n",
		len(plan.AffectedPackages), len(plan.CompilePackages), len(plan.TestOnlyPackages), len(plan.GuardPackages))
	if len(plan.FullReason) > 0 {
		fmt.Fprintf(&b, "- **Full reasons:** `%s`\n", strings.Join(plan.FullReason, "`, `"))
	}
	if len(plan.Unowned) > 0 {
		fmt.Fprintf(&b, "- **Unowned files:** `%s`\n", strings.Join(plan.Unowned, "`, `"))
	}
	b.WriteString("\n")
	_, err = f.WriteString(b.String())
	return err
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	cp := append([]float64(nil), xs...)
	slices.Sort(cp)
	mid := len(cp) / 2
	if len(cp)%2 == 1 {
		return cp[mid]
	}
	return (cp[mid-1] + cp[mid]) / 2
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
