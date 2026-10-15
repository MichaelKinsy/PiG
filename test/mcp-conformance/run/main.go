// Command run runs the official MCP client conformance suite against PiG (../client) and compares every check with the
// committed baseline (../baseline.json). It fails when a check that passed in the baseline no longer passes, or when a
// check fails that the baseline does not list. Known failures stay visible in the output. See ../README.md.
//
// Ports packages/coding-agent/test/mcp-conformance/run.ts.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// conformancePackage is the pinned upstream suite. Bumping it requires reviewing and regenerating the baseline.
const conformancePackage = "@modelcontextprotocol/conformance@0.2.0-alpha.11"

// modes are the protocol versions PiG negotiates. 2026-07-28 is a different (stateless) protocol PiG does not speak.
var modes = []string{"2025-03-26", "2025-06-18", "2025-11-25"}

const scenarioTimeout = 30 * time.Second

// ignoredStatuses are checks that are part of the suite's HTTP trace, not assertions.
var ignoredStatuses = map[string]bool{"INFO": true, "SKIPPED": true}

type scenarioResult struct {
	mode, scenario string
	// checks maps a check id to whether it passed. Repeated ids pass only when every instance passes.
	checks map[string]bool
	// messages are the failure messages by check id, for the report.
	messages map[string]string
}

type baselineScenario struct {
	Passing []string `json:"passing"`
	Failing []string `json:"failing"`
}

type baseline struct {
	Conformance string                                 `json:"conformance"`
	Modes       map[string]map[string]baselineScenario `json:"modes"`
}

type rawCheck struct {
	ID           any `json:"id"`
	Name         any `json:"name"`
	Status       any `json:"status"`
	Description  any `json:"description"`
	ErrorMessage any `json:"errorMessage"`
}

type commandResult struct {
	code           int
	stdout, stderr string
}

func runCommand(dir string, env []string, timeout time.Duration, name string, args ...string) (commandResult, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	ownProcessGroup(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		return commandResult{}, err
	}
	var timer *time.Timer
	if timeout > 0 {
		// The runner spawns the client and scenario servers; end the whole group.
		timer = time.AfterFunc(timeout, func() { killProcessGroup(cmd) })
	}
	err := cmd.Wait()
	if timer != nil {
		timer.Stop()
	}
	code := 0
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		code = exitErr.ExitCode()
		if code < 0 {
			code = 128
		}
	} else if err != nil {
		return commandResult{}, err
	}
	return commandResult{code, stdout.String(), stderr.String()}, nil
}

// conformanceEnv: the suite comes from npm with prebuilt files; nothing needs install scripts.
func conformanceEnv(extra ...string) []string {
	return append(os.Environ(), append([]string{"npm_config_ignore_scripts=true", "npm_config_yes=true"}, extra...)...)
}

var scenarioLine = regexp.MustCompile(`(?m)^\s+- (\S+)`)

func listScenarios(mode, workDir string) ([]string, error) {
	result, err := runCommand(workDir, conformanceEnv(), 0, "npx", "--yes", conformancePackage, "list", "--client", "--spec-version", mode)
	if err != nil {
		return nil, err
	}
	if result.code != 0 {
		message := result.stderr
		if message == "" {
			message = result.stdout
		}
		return nil, fmt.Errorf("Listing %s scenarios failed:\n%s", mode, message)
	}
	var scenarios []string
	for _, match := range scenarioLine.FindAllStringSubmatch(result.stdout, -1) {
		scenarios = append(scenarios, match[1])
	}
	return scenarios, nil
}

func findFile(dir, name string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if !entry.IsDir() && entry.Name() == name {
			return path
		}
		if entry.IsDir() {
			if found := findFile(path, name); found != "" {
				return found
			}
		}
	}
	return ""
}

func (r *scenarioResult) record(id string, passed bool, message string) {
	if previous, seen := r.checks[id]; !seen || previous {
		r.checks[id] = passed
	}
	if !passed && message != "" {
		if _, ok := r.messages[id]; !ok {
			r.messages[id] = message
		}
	}
}

func text(value any, fallback string) string {
	if value == nil {
		return fallback
	}
	return fmt.Sprint(value)
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

func runScenario(mode, scenario, launcher, workDir string, verbose bool) (*scenarioResult, error) {
	scenarioDir := filepath.Join(workDir, mode, unsafeName.ReplaceAllString(scenario, "-"))
	if err := os.MkdirAll(scenarioDir, 0o755); err != nil {
		return nil, err
	}
	reportPath := filepath.Join(scenarioDir, "pi-client.json")
	result := &scenarioResult{mode: mode, scenario: scenario, checks: map[string]bool{}, messages: map[string]string{}}
	run, err := runCommand(workDir, conformanceEnv("PIG_MCP_CONFORMANCE_REPORT="+reportPath), scenarioTimeout+30*time.Second, "npx",
		"--yes", conformancePackage, "client", "--command", launcher, "--scenario", scenario, "--spec-version", mode,
		"--timeout", fmt.Sprint(scenarioTimeout.Milliseconds()), "--output-dir", scenarioDir)
	if err != nil {
		return nil, err
	}
	if verbose {
		fmt.Fprint(os.Stderr, run.stderr)
	}
	checksPath := findFile(scenarioDir, "checks.json")
	if checksPath == "" {
		tail := strings.TrimSpace(run.stderr)
		result.record("runner", false, fmt.Sprintf("No checks.json (runner exit %d): %s", run.code, tail[max(0, len(tail)-2000):]))
		return result, nil
	}
	data, err := os.ReadFile(checksPath)
	if err != nil {
		return nil, err
	}
	var checks []rawCheck
	if err := json.Unmarshal(data, &checks); err != nil {
		return nil, fmt.Errorf("%s: %w", checksPath, err)
	}
	for _, check := range checks {
		status := strings.ToUpper(text(check.Status, "FAILURE"))
		if ignoredStatuses[status] {
			continue
		}
		id := text(check.ID, text(check.Name, "unnamed"))
		result.record(id, status == "SUCCESS", text(check.ErrorMessage, text(check.Description, "")))
	}
	// Whether PiG completed the scenario. Some scenarios expect the client to give up, so this is baselined like any
	// other check. The id stays `pi-client`, as upstream names it, so the two baselines compare line by line.
	var client struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	client.Error = "client wrote no report"
	if data, err := os.ReadFile(reportPath); err == nil {
		client.Error = ""
		if err := json.Unmarshal(data, &client); err != nil {
			return nil, fmt.Errorf("%s: %w", reportPath, err)
		}
	}
	result.record("pi-client", client.Success, client.Error)
	return result, nil
}

func baselinePathOf(here string) string { return filepath.Join(here, "baseline.json") }

// evaluate compares the results with the baseline and reports whether there is no regression.
func evaluate(results []*scenarioResult, expected baseline) bool {
	var regressions, fixed []string
	for _, result := range results {
		want, known := expected.Modes[result.mode][result.scenario]
		label := result.mode + " " + result.scenario
		ids := make([]string, 0, len(result.checks))
		for id := range result.checks {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			passed, message := result.checks[id], result.messages[id]
			if !passed && (!known || !slices.Contains(want.Failing, id)) {
				regressions = append(regressions, fmt.Sprintf("%s: %s fails%s", label, id, withMessage(message)))
			}
			if passed && known && slices.Contains(want.Failing, id) {
				fixed = append(fixed, label+": "+id)
			}
		}
		for _, id := range want.Passing {
			if _, reported := result.checks[id]; !reported {
				regressions = append(regressions, fmt.Sprintf("%s: %s was not reported", label, id))
			}
		}
	}
	for _, mode := range sortedKeys(expected.Modes) {
		for _, scenario := range sortedKeys(expected.Modes[mode]) {
			if !slices.ContainsFunc(results, func(r *scenarioResult) bool { return r.mode == mode && r.scenario == scenario }) {
				regressions = append(regressions, fmt.Sprintf("%s %s: scenario did not run", mode, scenario))
			}
		}
	}
	if len(fixed) > 0 {
		fmt.Printf("\nFixed since the baseline (run with --update-baseline):\n  %s\n", strings.Join(fixed, "\n  "))
	}
	if len(regressions) > 0 {
		fmt.Printf("\nRegressions:\n  %s\n", strings.Join(regressions, "\n  "))
		return false
	}
	fmt.Println("\nNo regressions against the baseline.")
	return true
}

func withMessage(message string) string {
	if message == "" {
		return ""
	}
	return ": " + message
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

var whitespaceRun = regexp.MustCompile(`\s+`)

func printResults(results []*scenarioResult) {
	for _, mode := range modes {
		first := true
		for _, result := range results {
			if result.mode != mode {
				continue
			}
			if first {
				fmt.Printf("\n%s\n", mode)
				first = false
			}
			failed := 0
			for _, passed := range result.checks {
				if !passed {
					failed++
				}
			}
			label := "pass"
			if failed > 0 {
				label = "FAIL"
			}
			fmt.Printf("  %s %s (%d passed, %d failed)\n", label, result.scenario, len(result.checks)-failed, failed)
			for _, id := range sortedKeys(result.checks) {
				if result.checks[id] {
					continue
				}
				message := whitespaceRun.ReplaceAllString(result.messages[id], " ")
				if len(message) > 300 {
					message = message[:300]
				}
				fmt.Printf("         %s%s\n", id, withMessage(message))
			}
		}
	}
}

type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ",") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }

func main() { os.Exit(realMain()) }

func realMain() int {
	var onlyModes, onlyScenarios stringList
	flag.Var(&onlyModes, "mode", "Only run this protocol version (repeatable): "+strings.Join(modes, ", "))
	flag.Var(&onlyScenarios, "scenario", "Only run this scenario (repeatable)")
	update := flag.Bool("update-baseline", false, "Write the results to baseline.json instead of comparing")
	keep := flag.Bool("keep-results", false, "Keep the upstream checks.json and client output")
	verbose := flag.Bool("verbose", false, "Print the upstream runner output")
	here := flag.String("dir", "test/mcp-conformance", "The directory with baseline.json, relative to the repository root")
	flag.Parse()
	if !supported {
		fmt.Fprintln(os.Stderr, "The conformance runner needs a POSIX shell.")
		return 1
	}
	selected := []string(onlyModes)
	if len(selected) == 0 {
		selected = slices.Clone(modes)
	}
	for _, mode := range selected {
		if !slices.Contains(modes, mode) {
			fmt.Fprintf(os.Stderr, "Unsupported mode %s. Supported: %s\n", mode, strings.Join(modes, ", "))
			return 1
		}
	}
	partial := len(onlyModes) > 0 || len(onlyScenarios) > 0
	if *update && partial {
		fmt.Fprintln(os.Stderr, "--update-baseline needs a full run, without --mode or --scenario.")
		return 1
	}
	wanted := func(scenario string) bool { return len(onlyScenarios) == 0 || slices.Contains(onlyScenarios, scenario) }

	workDir, err := os.MkdirTemp("", "pig-mcp-conformance-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if *keep {
		defer fmt.Printf("\nResults kept in %s\n", workDir)
	} else {
		defer func() { _ = os.RemoveAll(workDir) }()
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	client := filepath.Join(workDir, "client")
	build := exec.CommandContext(context.Background(), "go", "build", "-o", client, "./"+filepath.ToSlash(filepath.Join(*here, "client")))
	build.Dir, build.Stdout, build.Stderr = root, os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "building the client:", err)
		return 1
	}
	// The upstream runner splits --command on spaces, so use a launcher path without any.
	launcher := filepath.Join(workDir, "pig-client")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexec "+shellQuote(client)+" \"$@\"\n"), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	var results []*scenarioResult
	for _, mode := range selected {
		scenarios, err := listScenarios(mode, workDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		for _, scenario := range scenarios {
			if !wanted(scenario) {
				continue
			}
			fmt.Fprintf(os.Stderr, "%s %s\n", mode, scenario)
			result, err := runScenario(mode, scenario, launcher, workDir, *verbose)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			results = append(results, result)
		}
	}
	if len(results) == 0 {
		fmt.Fprintln(os.Stderr, "No scenarios matched.")
		return 1
	}
	printResults(results)

	path := baselinePathOf(filepath.Join(root, *here))
	if *update {
		data, err := marshalBaseline(results)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("\nWrote %s\n", path)
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "\nNo baseline.json. Run with --update-baseline and review it.")
		return 1
	}
	var expected baseline
	if err := json.Unmarshal(data, &expected); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
		return 1
	}
	if expected.Conformance != conformancePackage {
		fmt.Fprintf(os.Stderr, "\nbaseline.json is for %s, not %s. Regenerate it.\n", expected.Conformance, conformancePackage)
		return 1
	}
	if partial {
		scoped := baseline{Conformance: expected.Conformance, Modes: map[string]map[string]baselineScenario{}}
		for _, mode := range selected {
			scoped.Modes[mode] = map[string]baselineScenario{}
			for scenario, entry := range expected.Modes[mode] {
				if wanted(scenario) {
					scoped.Modes[mode][scenario] = entry
				}
			}
		}
		expected = scoped
	}
	if evaluate(results, expected) {
		return 0
	}
	return 1
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// marshalBaseline writes the results as the baseline, with tab indentation and in the order the suite lists its modes and
// scenarios, as upstream does.
func marshalBaseline(results []*scenarioResult) ([]byte, error) {
	encode := func(value any) (string, error) {
		var out bytes.Buffer
		encoder := json.NewEncoder(&out)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(value); err != nil {
			return "", err
		}
		return strings.TrimSuffix(out.String(), "\n"), nil
	}
	list := func(ids []string, indent string) (string, error) {
		if len(ids) == 0 {
			return "[]", nil
		}
		quoted := make([]string, len(ids))
		for i, id := range ids {
			q, err := encode(id)
			if err != nil {
				return "", err
			}
			quoted[i] = indent + "\t" + q
		}
		return "[\n" + strings.Join(quoted, ",\n") + "\n" + indent + "]", nil
	}
	conformance, err := encode(conformancePackage)
	if err != nil {
		return nil, err
	}
	var modeBlocks []string
	for _, mode := range modes {
		var blocks []string
		for _, result := range results {
			if result.mode != mode {
				continue
			}
			var passing, failing []string
			for _, id := range sortedKeys(result.checks) {
				if result.checks[id] {
					passing = append(passing, id)
				} else {
					failing = append(failing, id)
				}
			}
			passList, err := list(passing, "\t\t\t\t")
			if err != nil {
				return nil, err
			}
			failList, err := list(failing, "\t\t\t\t")
			if err != nil {
				return nil, err
			}
			name, err := encode(result.scenario)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, "\t\t\t"+name+": {\n\t\t\t\t\"passing\": "+passList+",\n\t\t\t\t\"failing\": "+failList+"\n\t\t\t}")
		}
		if len(blocks) > 0 {
			modeBlocks = append(modeBlocks, "\t\t\""+mode+"\": {\n"+strings.Join(blocks, ",\n")+"\n\t\t}")
		}
	}
	return []byte("{\n\t\"conformance\": " + conformance + ",\n\t\"modes\": {\n" + strings.Join(modeBlocks, ",\n") + "\n\t}\n}\n"), nil
}
