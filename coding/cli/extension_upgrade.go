package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// pig additive (D109): pig compiles Go extensions against its own SDK, so an SDK release can break an extension written for an older one. Pi loads extension source in process and has no counterpart.

// extensionDrift is one extension that failed to build only because it was written for an older SDK.
type extensionDrift struct {
	Name   string
	Source string
	Config subprocess.ExtConfig
	Drift  []upgrade.Drift
}

// sdkDriftOf returns the extensions whose load errors are SDK drift in their own source. A packed cell reports one build failure for every extension in it, so a drift counts for an extension only when a file it names is in that extension's source.
func sdkDriftOf(errs []error, configs []subprocess.ExtConfig) []extensionDrift {
	var found []extensionDrift
	for _, err := range errs {
		loadErr, ok := errors.AsType[*subprocess.ExtensionLoadError](err)
		if !ok {
			continue
		}
		failure, ok := errors.AsType[*runtimecell.BuildFailure](loadErr.Err)
		if !ok || len(failure.Drift) == 0 {
			continue
		}
		for _, config := range configs {
			if config.Name != loadErr.Name || config.Source == "" {
				continue
			}
			roots := append([]string{config.Source}, config.GoWorkspaceModules...)
			var own []upgrade.Drift
			for _, drift := range failure.Drift {
				if pathWithinAny(drift.File, roots) {
					own = append(own, drift)
				}
			}
			// Two copies of one extension share its name: the drift belongs to the copy whose source holds the failing file.
			if len(own) == 0 {
				continue
			}
			if !slices.ContainsFunc(found, func(item extensionDrift) bool { return item.Source == config.Source }) {
				found = append(found, extensionDrift{Name: config.Name, Source: config.Source, Config: config, Drift: own})
			}
			break
		}
	}
	return found
}

func pathWithinAny(path string, roots []string) bool {
	path = canonicalPath(path)
	for _, root := range roots {
		root = canonicalPath(root)
		if path == root || strings.HasPrefix(path, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func canonicalPath(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	if physical, err := filepath.EvalSymlinks(path); err == nil {
		path = physical
	}
	return filepath.Clean(path)
}

var plainShellWord = lazyregexp.New(`^[A-Za-z0-9_@%+=:,./\\-]+$`)

func commandWord(word string) string {
	if plainShellWord.MatchString(word) {
		return word
	}
	if runtime.GOOS == "windows" {
		return `"` + strings.ReplaceAll(word, `"`, `\"`) + `"`
	}
	return "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
}

// extensionUpgradeCommand is the exact command that upgrades the extensions.
func extensionUpgradeCommand(items []extensionDrift) string {
	words := []string{codingagent.AppName, "extension", "upgrade"}
	for _, item := range items {
		words = append(words, commandWord(item.Source))
	}
	return strings.Join(words, " ")
}

// sdkDriftNotice names each extension written for an older SDK, each changed API with its old and new shape, and the command that upgrades them.
func sdkDriftNotice(items []extensionDrift) string {
	var out strings.Builder
	for _, item := range items {
		_, _ = fmt.Fprintf(&out, "Extension %q was written for an older SDK:\n", item.Name)
		for _, drift := range item.Drift {
			line := "  " + drift.String()
			if !drift.Rewrites {
				line += ": needs a manual change"
			}
			out.WriteString(line + "\n")
		}
	}
	_, _ = fmt.Fprintf(&out, "Run: %s\n", extensionUpgradeCommand(items))
	return out.String()
}

// extensionUpgradeStatus is the outcome of upgrading one extension.
type extensionUpgradeStatus string

const (
	upgradeUpToDate extensionUpgradeStatus = "up to date"
	upgradeUpgraded extensionUpgradeStatus = "upgraded"
	upgradePreview  extensionUpgradeStatus = "would upgrade"
	upgradeManual   extensionUpgradeStatus = "needs manual changes"
	upgradeFailed   extensionUpgradeStatus = "failed"
	upgradeSkipped  extensionUpgradeStatus = "skipped"
)

// extensionUpgradeReport is what an upgrade did to one extension.
type extensionUpgradeReport struct {
	Name   string
	Source string
	Status extensionUpgradeStatus
	// Note explains a skipped or failed extension.
	Note   string
	Result *upgrade.Result
	// Backup is the directory that holds the files as they were.
	Backup string
	// Built is set when the upgraded extension compiled.
	Built bool
}

// Ok reports whether the extension builds against the current SDK after the upgrade.
func (r extensionUpgradeReport) Ok() bool {
	return r.Status == upgradeUpToDate || (r.Status == upgradeUpgraded && r.Built)
}

// extensionUpgrader rewrites Go extensions written for an older SDK.
type extensionUpgrader struct {
	configRoot string
	// analyze reads the extension's types and plans the rewrites; rebuild compiles it. They are seams for tests.
	analyze func(ctx context.Context, config subprocess.ExtConfig) (*upgrade.Result, error)
	rebuild func(ctx context.Context, config subprocess.ExtConfig) error
	now     func() time.Time
}

func newExtensionUpgrader(configRoot string) *extensionUpgrader {
	u := &extensionUpgrader{configRoot: configRoot, now: time.Now} //portlint:allow clock the backup directory is named for the real time of the upgrade; Pi has no counterpart (D109)
	u.analyze = u.analyzeExtension
	u.rebuild = u.rebuildExtension
	return u
}

func (u *extensionUpgrader) sdkDir() string {
	return filepath.Join(u.configRoot, "state", "pigsdk", "sdk")
}

func (u *extensionUpgrader) cacheRoot() string { return filepath.Join(u.configRoot, "cache") }

func isGoSourceExtension(config subprocess.ExtConfig) bool {
	return config.RuntimeLanguage == "go" && config.Source != ""
}

func isGoFactoryExtension(config subprocess.ExtConfig) bool {
	return isGoSourceExtension(config) && config.EntrypointKind == "factory" && config.Package != "" && config.Factory == "Extension"
}

func goExtensionOf(config subprocess.ExtConfig) runtimecell.GoExtension {
	hash := config.ContentHash
	if hash == "" {
		hash = config.Source
	}
	return runtimecell.GoExtension{
		Name: config.Name, Root: config.Source, ModulePath: config.ModulePath, Package: config.Package, Factory: config.Factory,
		Hash: hash, WorkspaceModules: slices.Clone(config.GoWorkspaceModules),
	}
}

// analyzeExtension type checks the extension the way its build sees it and plans the rewrites.
func (u *extensionUpgrader) analyzeExtension(ctx context.Context, config subprocess.ExtConfig) (*upgrade.Result, error) {
	var result *upgrade.Result
	plan := func(options upgrade.LoadOptions) error {
		session, err := upgrade.Open(ctx, options)
		if err != nil {
			return err
		}
		result, err = session.Plan()
		return err
	}
	roots := append([]string{config.Source}, config.GoWorkspaceModules...)
	if isGoFactoryExtension(config) {
		err := runtimecell.WithGoBuildModule(ctx, goExtensionOf(config), u.sdkDir(), func(module runtimecell.GoBuildModule) error {
			return plan(upgrade.LoadOptions{Dir: module.Dir, Roots: roots, Patterns: []string{"."}, Command: module.Command, Env: module.Env})
		})
		return result, err
	}
	builder := subprocess.NewBuilderWithConfigRoot(filepath.Join(u.cacheRoot(), "ext"), u.configRoot)
	err := builder.WithGoListing(config.Source, func(listing subprocess.GoListing) error {
		return plan(upgrade.LoadOptions{Dir: config.Source, Roots: roots, Flags: listing.Flags, Command: listing.Command, Env: listing.Env})
	})
	return result, err
}

// rebuildExtension compiles the extension as startup does, so the result is in the build cache.
func (u *extensionUpgrader) rebuildExtension(ctx context.Context, config subprocess.ExtConfig) error {
	if isGoFactoryExtension(config) {
		_, err := runtimecell.BuildGoPackedCellWithSDKRoot(ctx, u.cacheRoot(), "upgrade-"+config.Name, []runtimecell.GoExtension{goExtensionOf(config)}, u.sdkDir())
		return err
	}
	_, err := subprocess.NewBuilderWithConfigRoot(filepath.Join(u.cacheRoot(), "ext"), u.configRoot).BuildContext(ctx, config.Name, config.Source)
	return err
}

// upgrade plans, backs up, rewrites and rebuilds one extension. With dryRun it plans only. It never touches a file the plan does not rewrite.
func (u *extensionUpgrader) upgrade(ctx context.Context, config subprocess.ExtConfig, dryRun bool) extensionUpgradeReport {
	report := extensionUpgradeReport{Name: config.Name, Source: config.Source}
	if !isGoSourceExtension(config) {
		report.Status, report.Note = upgradeSkipped, "not a Go source extension: the SDK upgrade rules cover Go extensions"
		return report
	}
	result, err := u.analyze(ctx, config)
	if err != nil {
		report.Status, report.Note = upgradeFailed, fmt.Sprintf("read the extension: %v", err)
		return report
	}
	report.Result = result
	switch {
	case !result.Changed() && len(result.Remaining) == 0 && len(result.Skipped) == 0 && len(result.Errors) == 0:
		report.Status = upgradeUpToDate
		return report
	case !result.Changed():
		report.Status = upgradeManual
		if len(result.Remaining) == 0 && len(result.Skipped) == 0 {
			report.Status, report.Note = upgradeFailed, "the extension has compile errors that are not SDK changes"
		}
		return report
	case dryRun:
		report.Status = upgradePreview
		return report
	}
	if ctx.Err() != nil {
		report.Status, report.Note = upgradeFailed, fmt.Sprintf("cancelled before any file changed: %v", ctx.Err())
		return report
	}
	if report.Backup, err = u.backup(config, result); err != nil {
		report.Status, report.Note = upgradeFailed, fmt.Sprintf("back up the files: %v", err)
		return report
	}
	for _, change := range result.Files {
		if err := writeFileKeepingMode(change.Path, change.After); err != nil {
			report.Status, report.Note = upgradeFailed, fmt.Sprintf("write %s: %v (the original is in %s)", change.Path, err, report.Backup)
			return report
		}
	}
	report.Status = upgradeUpgraded
	if err := u.rebuild(ctx, config); err != nil {
		report.Note = rebuildNote(err)
		return report
	}
	report.Built = true
	if len(result.Remaining) > 0 || len(result.Skipped) > 0 {
		report.Status = upgradeManual
	}
	return report
}

func rebuildNote(err error) string {
	if failure, ok := errors.AsType[*runtimecell.BuildFailure](err); ok && len(failure.Drift) > 0 {
		return "the upgraded extension still uses changed APIs: " + failure.Error()
	}
	return "the upgraded extension does not build: " + err.Error()
}

type backupManifest struct {
	Extension string       `json:"extension"`
	Source    string       `json:"source"`
	Created   string       `json:"created"`
	Files     []backupFile `json:"files"`
}

type backupFile struct {
	Path   string `json:"path"`
	Backup string `json:"backup"`
}

// backup copies each file the plan rewrites into a directory under the configuration root, with a manifest that maps the copies back.
func (u *extensionUpgrader) backup(config subprocess.ExtConfig, result *upgrade.Result) (string, error) {
	created := u.now().UTC()
	name := regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(config.Name, "_")
	dir, err := newBackupDir(filepath.Join(u.configRoot, "state", "extension-upgrade"), created.Format("20060102T150405Z")+"-"+name)
	if err != nil {
		return "", err
	}
	manifest := backupManifest{Extension: config.Name, Source: config.Source, Created: created.Format(time.RFC3339)}
	for _, change := range result.Files {
		relative, err := filepath.Rel(config.Source, change.Path)
		if err != nil || strings.HasPrefix(relative, "..") {
			sum := sha256.Sum256([]byte(change.Path))
			relative = filepath.Join("external", hex.EncodeToString(sum[:6])+"-"+filepath.Base(change.Path))
		}
		target := filepath.Join(dir, "files", relative)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(target, change.Before, 0o600); err != nil {
			return "", err
		}
		manifest.Files = append(manifest.Files, backupFile{Path: change.Path, Backup: target})
	}
	data, err := json.MarshalIndent(manifest, "", "  ") //portlint:allow jsonescape the manifest is PiG-only backup metadata read back by Go, not a Pi-compatible file
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), append(data, '\n'), 0o600); err != nil {
		return "", err
	}
	return dir, nil
}

// newBackupDir creates a backup directory that no earlier upgrade owns: base, or base-2, base-3 and so on when two upgrades of extensions with one name start in the same second.
func newBackupDir(parent, base string) (string, error) {
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", err
	}
	for n := 1; ; n++ {
		dir := filepath.Join(parent, base)
		if n > 1 {
			dir = fmt.Sprintf("%s-%d", dir, n)
		}
		err := os.Mkdir(dir, 0o700)
		if err == nil {
			return dir, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
}

// writeFileKeepingMode replaces a file through a temporary file in its directory, so a failed write leaves the original.
func writeFileKeepingMode(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".pig-upgrade-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	_, writeErr := temporary.Write(data)
	if closeErr := temporary.Close(); writeErr != nil || closeErr != nil {
		_ = os.Remove(name)
		return errors.Join(writeErr, closeErr)
	}
	if err := os.Chmod(name, mode); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// printUpgradeReport writes one extension's outcome: the diff of each file the plan changes, what the author still has to change, and the result of the rebuild.
func printUpgradeReport(w io.Writer, report extensionUpgradeReport, showDiff bool) {
	_, _ = fmt.Fprintf(w, "%s: %s\n", report.Name, report.Status)
	if report.Note != "" {
		_, _ = fmt.Fprintf(w, "  %s\n", report.Note)
	}
	result := report.Result
	if result == nil {
		return
	}
	for _, change := range result.Files {
		counts := map[string]int{}
		for _, rewrite := range change.Rewrites {
			counts[rewrite.Symbol]++
		}
		symbols := make([]string, 0, len(counts))
		for symbol := range counts {
			symbols = append(symbols, symbol)
		}
		slices.Sort(symbols)
		_, _ = fmt.Fprintf(w, "  %s: %d change(s) for %s\n", change.Path, len(change.Rewrites), strings.Join(symbols, ", "))
		if showDiff {
			label := change.Path
			if relative, err := filepath.Rel(report.Source, change.Path); err == nil && !strings.HasPrefix(relative, "..") {
				label = filepath.ToSlash(relative)
			}
			_, _ = fmt.Fprint(w, upgrade.Diff(label, change.Before, change.After))
		}
	}
	if report.Backup != "" {
		_, _ = fmt.Fprintf(w, "  original files: %s\n", report.Backup)
	}
	for _, drift := range result.Remaining {
		_, _ = fmt.Fprintf(w, "  still to change by hand: %s\n", drift)
		if rule, ok := upgrade.RuleByID(drift.Rule); ok {
			_, _ = fmt.Fprintf(w, "    %s\n", rule.Remedy)
		}
	}
	for _, skipped := range result.Skipped {
		_, _ = fmt.Fprintf(w, "  %s:%d: edit by hand (%s): %s\n", skipped.File, skipped.Line, skipped.Symbol, skipped.Reason)
	}
	for _, message := range result.Errors {
		_, _ = fmt.Fprintf(w, "  compile error: %s\n", message)
	}
	switch {
	case report.Status == upgradeUpgraded && report.Built:
		_, _ = fmt.Fprintln(w, "  rebuilt: ok")
	case report.Status == upgradeManual && report.Built:
		_, _ = fmt.Fprintln(w, "  rebuilt: ok")
	}
}
