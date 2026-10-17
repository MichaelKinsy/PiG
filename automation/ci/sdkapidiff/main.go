// Command sdkapidiff fails when the extension SDK makes an incompatible API change since its last release that has no upgrade rule or no release note.
//
// The baseline is the SDK version the root module requires. The gate compares every package of that release with extensions/sdk with apidiff in module mode. Every incompatible symbol needs a rule in extensions/sdk/upgrade (a rewrite, or a diagnosis with a remedy) and a release note that names the symbol in backquotes in changelog.d or in CHANGELOG.md.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade"
)

const sdkModule = "github.com/MichaelKinsy/PiG/extensions/sdk"

func main() {
	root, err := os.Getwd()
	if err != nil {
		fail(err)
	}
	problems, baseline, err := run(root)
	if err != nil {
		fail(err)
	}
	if len(problems) == 0 {
		fmt.Printf("sdk-apidiff: clean (every incompatible change since %s has an upgrade rule and a release note)\n", baseline)
		return
	}
	fmt.Fprintf(os.Stderr, "sdk-apidiff: incompatible SDK changes since %s without an upgrade rule or release note:\n", baseline)
	for _, problem := range problems {
		fmt.Fprintln(os.Stderr, "  - "+problem)
	}
	fmt.Fprintln(os.Stderr, "Add a rule to extensions/sdk/upgrade/rules.go and name the symbol in backquotes in a changelog.d fragment.")
	os.Exit(1)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "sdk-apidiff:", err)
	os.Exit(2)
}

func run(root string) (problems []string, baseline string, err error) {
	baseline, err = requiredSDKVersion(root)
	if err != nil {
		return nil, "", err
	}
	baselineDir, err := downloadModule(root, sdkModule+"@"+baseline)
	if err != nil {
		return nil, baseline, err
	}
	tool, err := apidiffTool(root)
	if err != nil {
		return nil, baseline, err
	}
	incompatible, err := incompatibleChanges(tool, baselineDir, filepath.Join(root, "extensions", "sdk"))
	if err != nil {
		return nil, baseline, err
	}
	notes, err := releaseNotes(root)
	if err != nil {
		return nil, baseline, err
	}
	return evaluate(incompatible, upgrade.Covers, notes), baseline, nil
}

// goCommand runs the go command with the workspace off, so the baseline resolves from the module cache.
func goCommand(dir string, args ...string) (string, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String(), nil
}

func requiredSDKVersion(root string) (string, error) {
	out, err := goCommand(root, "list", "-m", "-f", "{{.Version}}", sdkModule)
	if err != nil {
		return "", err
	}
	version := strings.TrimSpace(out)
	if version == "" {
		return "", fmt.Errorf("the root module does not require %s", sdkModule)
	}
	return version, nil
}

func downloadModule(root, module string) (string, error) {
	out, err := goCommand(root, "mod", "download", "-json", module)
	if err != nil {
		return "", err
	}
	var info struct{ Dir string }
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return "", fmt.Errorf("go mod download %s: %w", module, err)
	}
	if info.Dir == "" {
		return "", fmt.Errorf("go mod download %s reported no directory", module)
	}
	return info.Dir, nil
}

func apidiffTool(root string) (string, error) {
	out, err := goCommand(root, "tool", "-n", "apidiff")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// upgradePackagePrefix names the symbols of the upgrade package in a module report. The package is the rule table that the pig built from this tree imports, not an API extensions call, so its own changes need no upgrade rule.
const upgradePackagePrefix = "- ./upgrade"

// incompatibleChanges returns the apidiff lines for incompatible changes between the SDK module in oldDir and the one in newDir. It compares every package of the module: the root package reports `Context.IsIdle`, and a subpackage reports `./json.Marshal`.
func incompatibleChanges(tool, oldDir, newDir string) ([]string, error) {
	scratch, err := os.MkdirTemp("", "sdkapidiff-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	export := func(dir, name string) (string, error) {
		file := filepath.Join(scratch, name)
		cmd := exec.Command(tool, "-m", "-w", file, sdkModule)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("apidiff -w in %s: %w\n%s", dir, err, out)
		}
		return file, nil
	}
	oldAPI, err := export(oldDir, "old.api")
	if err != nil {
		return nil, err
	}
	newAPI, err := export(newDir, "new.api")
	if err != nil {
		return nil, err
	}
	out, err := exec.Command(tool, "-m", "-incompatible", oldAPI, newAPI).Output()
	if err != nil {
		return nil, fmt.Errorf("apidiff -incompatible: %w", err)
	}
	var lines []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, upgradePackagePrefix+".") && !strings.HasPrefix(line, upgradePackagePrefix+":") {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

// releaseNotes joins the release-note fragments and the changelog, where a note names an API in backquotes.
func releaseNotes(root string) (string, error) {
	files, err := filepath.Glob(filepath.Join(root, "changelog.d", "*.md"))
	if err != nil {
		return "", err
	}
	files = append(files, filepath.Join(root, "CHANGELOG.md"))
	var notes strings.Builder
	for _, file := range files {
		data, err := os.ReadFile(file)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		notes.Write(data)
		notes.WriteByte('\n')
	}
	return notes.String(), nil
}

var symbolPattern = regexp.MustCompile(`^- (.+?): `)

// evaluate returns one problem per incompatible apidiff line whose symbol has no rule or no release note.
func evaluate(incompatible []string, covered func(symbol string) bool, notes string) []string {
	var problems []string
	for _, line := range incompatible {
		match := symbolPattern.FindStringSubmatch(line)
		if match == nil {
			problems = append(problems, "unreadable apidiff line: "+line)
			continue
		}
		symbol := match[1]
		var missing []string
		if !covered(symbol) {
			missing = append(missing, "upgrade rule")
		}
		if !noted(notes, symbol) {
			missing = append(missing, "release note")
		}
		if len(missing) > 0 {
			problems = append(problems, fmt.Sprintf("%s: no %s (%s)", symbol, strings.Join(missing, " and "), strings.TrimPrefix(line, "- "+symbol+": ")))
		}
	}
	slices.Sort(problems)
	return problems
}

// noted reports whether a release note names the symbol in backquotes, with or without a leading "(*T)." receiver. A subpackage symbol such as "./json.Marshal" is named without its "./".
func noted(notes, symbol string) bool {
	symbol = strings.TrimPrefix(symbol, "./")
	names := []string{symbol}
	if rest, ok := strings.CutPrefix(symbol, "(*"); ok {
		if receiver, method, found := strings.Cut(rest, ")."); found {
			names = append(names, receiver+"."+method)
		}
	}
	for _, name := range names {
		if strings.Contains(notes, "`"+name+"`") || strings.Contains(notes, "`"+name+"(") {
			return true
		}
	}
	return false
}
