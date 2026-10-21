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
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
)

// pig additive (D109): pig compiles Go extensions against its own SDK, so an SDK release can break an extension written for an older one. Pi loads extension source in process and has no counterpart.

// extensionDrift is one extension that failed to build and is written for an older SDK.
type extensionDrift struct {
	Name   string
	Source string
	Config subprocess.ExtConfig
	Drift  []upgrade.Drift
	// Plan is the type-checked plan of the upgrade when startup read the extension's types, else nil.
	Plan *upgrade.Result
}

// sdkDriftOf returns the extensions that failed to load and are written for an older SDK. A packed cell reports one build failure for every extension in it, so a drift counts for an extension only when a file it names is in that extension's source.
// The go command prints at most ten compile errors, a recorded failure can predate the classification, and an extension can have compile errors of its own beside the SDK changes. So with an upgrader, every Go source extension whose build failed is type checked against the current SDK with the rules the upgrade rewrites by, and it is offered when the plan finds any changed API it uses. known lists the extensions already found; they are not read again.
func sdkDriftOf(ctx context.Context, upgrader *extensionUpgrader, errs []error, configs []subprocess.ExtConfig, known []extensionDrift) []extensionDrift {
	var found []extensionDrift
	seen := func(source string) bool {
		same := func(item extensionDrift) bool { return item.Source == source }
		return slices.ContainsFunc(found, same) || slices.ContainsFunc(known, same)
	}
	for _, err := range errs {
		loadErr, ok := errors.AsType[*subprocess.ExtensionLoadError](err)
		if !ok {
			continue
		}
		failure, ok := errors.AsType[*runtimecell.BuildFailure](loadErr.Err)
		if !ok {
			continue
		}
		for _, config := range configs {
			if config.Name != loadErr.Name || config.Source == "" || seen(config.Source) {
				continue
			}
			roots := append([]string{config.Source}, config.GoWorkspaceModules...)
			var own []upgrade.Drift
			for _, drift := range failure.Drift {
				if pathWithinAny(drift.File, roots) {
					own = append(own, drift)
				}
			}
			var plan *upgrade.Result
			if upgrader != nil && isGoSourceExtension(config) {
				if result, err := upgrader.analyze(ctx, config); err == nil {
					if drift := result.Drift(); len(drift) > 0 {
						own, plan = drift, result
					}
				}
			}
			// Two copies of one extension share its name: the drift belongs to the copy whose source holds the failing file, or whose types show it.
			if len(own) == 0 {
				continue
			}
			found = append(found, extensionDrift{Name: config.Name, Source: config.Source, Config: config, Drift: own, Plan: plan})
			break
		}
	}
	return found
}

// withUpgradeHints replaces the load error of each extension in drift with one that names the changed APIs and the command that upgrades it, so the line says what to do where the go command's own message says which line of code failed.
func withUpgradeHints(errs []error, drift []extensionDrift) []error {
	if len(drift) == 0 {
		return errs
	}
	hinted := make([]error, len(errs))
	for i, err := range errs {
		hinted[i] = err
		loadErr, ok := errors.AsType[*subprocess.ExtensionLoadError](err)
		if !ok {
			continue
		}
		for _, item := range drift {
			roots := append([]string{item.Source}, item.Config.GoWorkspaceModules...)
			if item.Name == loadErr.Name && (loadErr.Path == item.Source || pathWithinAny(loadErr.Path, roots)) {
				hinted[i] = &subprocess.ExtensionLoadError{Name: loadErr.Name, Path: loadErr.Path, Err: errors.New(upgradeHint(item))}
				break
			}
		}
	}
	return hinted
}

// upgradeHint is the one line a load error of a drifted extension reads: the first changed APIs and the exact command.
func upgradeHint(item extensionDrift) string {
	symbols := make([]string, len(item.Drift))
	for i, drift := range item.Drift {
		symbols[i] = drift.Symbol
	}
	slices.Sort(symbols)
	if len(symbols) > 2 {
		symbols = append(symbols[:2:2], fmt.Sprintf("and %d more", len(item.Drift)-2))
	}
	return "go build: written for an older SDK (" + strings.Join(symbols, ", ") + " changed); run: " + extensionUpgradeCommand([]extensionDrift{item})
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

// plainPOSIXWord and plainWindowsWord are the words the shell of the platform reads as written: "\" is plain only on Windows, where it separates path elements; a POSIX shell reads it as an escape.
var (
	plainPOSIXWord   = lazyregexp.New(`^[A-Za-z0-9_@%+=:,./-]+$`)
	plainWindowsWord = lazyregexp.New(`^[A-Za-z0-9_@%+=:,./\\-]+$`)
)

// backupNameUnsafe is a run of characters a backup directory name replaces with "_".
var backupNameUnsafe = lazyregexp.New(`[^A-Za-z0-9._-]+`)

func commandWord(word string) string { return commandWordFor(runtime.GOOS, word) }

// commandWordFor is word as the shell of goos reads it: unchanged when plain, else quoted.
func commandWordFor(goos, word string) string {
	if goos == "windows" {
		if plainWindowsWord.MatchString(word) {
			return word
		}
		return `"` + strings.ReplaceAll(word, `"`, `\"`) + `"`
	}
	if plainPOSIXWord.MatchString(word) {
		return word
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

// sdkDriftNotice names each extension written for an older SDK, each changed API with its old and new shape, what the upgrade leaves for you to change with the file and line, and the command that upgrades them.
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
			if drift.Rewrites || drift.File == "" {
				continue
			}
			if rule, ok := upgrade.RuleByID(drift.Rule); ok {
				_, _ = fmt.Fprintf(&out, "    %s:%d: %s\n", drift.File, drift.Line, rule.Remedy)
			}
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
	// upgradePartial is a real run that applied every rewrite with one safe form and left uses for the author: the extension builds once they are made.
	upgradePartial extensionUpgradeStatus = "partly upgraded"
	// upgradePreview is a dry run whose rewritten extension builds; upgradePreviewPartial one that builds once the listed uses are made.
	upgradePreview        extensionUpgradeStatus = "would upgrade"
	upgradePreviewPartial extensionUpgradeStatus = "would partly upgrade"
	upgradeManual         extensionUpgradeStatus = "needs manual changes"
	upgradeFailed         extensionUpgradeStatus = "failed"
	upgradeSkipped        extensionUpgradeStatus = "skipped"
)

// extensionUpgradeReport is what an upgrade did to one extension.
type extensionUpgradeReport struct {
	Name   string
	Source string
	Status extensionUpgradeStatus
	// Note explains a skipped or failed extension.
	Note   string
	Result *upgrade.Result
	// Unexpected are the build errors of the rewritten extension that the plan does not list.
	Unexpected []string
	// Backup is the directory that holds the files as they were.
	Backup string
	// Built is set when the upgraded extension compiled.
	Built bool
}

// Ok reports whether the extension builds against the current SDK after the upgrade.
func (r extensionUpgradeReport) Ok() bool {
	return r.Status == upgradeUpToDate || (r.Status == upgradeUpgraded && r.Built)
}

// rewriteCheck is the outcome of compiling an extension with a plan's rewrites applied.
type rewriteCheck struct {
	// Built is set when the rewritten extension compiled and linked.
	Built bool
	// Unexpected are the compile errors the plan neither rewrote nor listed, or the build's own failure when it printed none.
	Unexpected []string
}

// extensionUpgrader rewrites Go extensions written for an older SDK.
type extensionUpgrader struct {
	configRoot string
	// analyze reads the extension's types and plans the rewrites; check compiles the extension with them applied in a temporary directory; rebuild compiles it as startup does. They are seams for tests.
	analyze func(ctx context.Context, config subprocess.ExtConfig) (*upgrade.Result, error)
	check   func(ctx context.Context, config subprocess.ExtConfig, result *upgrade.Result) (rewriteCheck, error)
	rebuild func(ctx context.Context, config subprocess.ExtConfig) error
	now     func() time.Time
}

func newExtensionUpgrader(configRoot string) *extensionUpgrader {
	u := &extensionUpgrader{configRoot: configRoot, now: time.Now} //portlint:allow clock the backup directory is named for the real time of the upgrade; Pi has no counterpart (D109)
	u.analyze = u.analyzeExtension
	u.check = u.checkRewrite
	u.rebuild = u.rebuildExtension
	return u
}

// sdkDir is the Go SDK this binary stages under the upgrader's config root, or "" when it embeds none and the build resolves the SDK elsewhere.
func (u *extensionUpgrader) sdkDir() string {
	dir, err := pigsdklock.StagedSDKDir(u.configRoot, "go")
	if err != nil {
		return ""
	}
	return dir
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

// rebuildExtension compiles the extension as startup does, so the result is in the build cache.
func (u *extensionUpgrader) rebuildExtension(ctx context.Context, config subprocess.ExtConfig) error {
	if isGoFactoryExtension(config) {
		_, err := runtimecell.BuildGoPackedCellWithSDKRoot(ctx, u.cacheRoot(), "upgrade-"+config.Name, []runtimecell.GoExtension{goExtensionOf(config)}, u.sdkDir())
		return err
	}
	_, err := subprocess.NewBuilderWithConfigRoot(filepath.Join(u.cacheRoot(), "ext"), u.configRoot).BuildContext(ctx, config.Name, config.Source)
	return err
}

// upgrade plans, backs up, rewrites and rebuilds one extension. With dryRun it plans and builds the result in a temporary directory, and writes nothing. It applies every rewrite that has one safe form and lists the rest, with their file, line and form. It never touches a file the plan does not rewrite. A rewrite that would add a compile error of its own is not written; when a write or the rebuild fails for a reason the plan does not list, every file it changed is restored.
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
	leftovers := len(result.Remaining) > 0 || len(result.Skipped) > 0 || len(result.Errors) > 0
	switch {
	case !result.Changed() && !leftovers:
		report.Status = upgradeUpToDate
		return report
	case !result.Changed():
		report.Status = upgradeManual
		if len(result.Remaining) == 0 && len(result.Skipped) == 0 {
			report.Status, report.Note = upgradeFailed, "the extension has compile errors that are not SDK changes"
		}
		return report
	}
	// The build of the result proves what the report promises. A real run with nothing left for the author is proved by its own rebuild, which restores the files when it fails.
	var check rewriteCheck
	if dryRun || leftovers {
		if check, err = u.check(ctx, config, result); err != nil {
			report.Status, report.Note = upgradeFailed, fmt.Sprintf("build the rewritten extension: %v", err)
			return report
		}
		if !check.Built && len(check.Unexpected) > 0 {
			report.Status, report.Unexpected = upgradeFailed, check.Unexpected
			report.Note = "the rewritten extension would not build for a reason that is not an SDK change the upgrade lists; no file was changed"
			return report
		}
	}
	if dryRun {
		report.Status = upgradePreview
		if !check.Built {
			report.Status = upgradePreviewPartial
		}
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
	written := make([]upgrade.FileChange, 0, len(result.Files))
	for _, change := range result.Files {
		if err := writeFileKeepingMode(change.Path, change.After); err != nil {
			report.Status, report.Note = upgradeFailed, fmt.Sprintf("write %s: %v", change.Path, err)+restoreFiles(&report, written)
			return report
		}
		written = append(written, change)
	}
	if err := u.rebuild(ctx, config); err != nil {
		if leftovers && !check.Built {
			// The build fails on the uses the report lists, and on nothing else: the safe rewrites stay.
			report.Status = upgradePartial
			return report
		}
		report.Status, report.Note = upgradeFailed, rebuildNote(err)+restoreFiles(&report, written)
		return report
	}
	report.Status, report.Built = upgradeUpgraded, true
	if leftovers {
		report.Status = upgradeManual
	}
	return report
}

// restoreFiles writes back the original of each file an upgrade changed and returns the end of the report's note. With every file restored the backup holds nothing more, so it is removed.
func restoreFiles(report *extensionUpgradeReport, changed []upgrade.FileChange) string {
	var failed []error
	for _, change := range changed {
		if change.Created {
			if err := os.Remove(change.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
				failed = append(failed, err)
			}
			continue
		}
		if err := writeFileKeepingMode(change.Path, change.Before); err != nil {
			failed = append(failed, err)
		}
	}
	if len(failed) > 0 {
		return fmt.Sprintf("; restoring the original files failed (%v): they are in %s", errors.Join(failed...), report.Backup)
	}
	if err := os.RemoveAll(report.Backup); err == nil {
		report.Backup = ""
	}
	if len(changed) == 0 {
		return "; no file changed"
	}
	return "; the original files are restored"
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
	Path string `json:"path"`
	// Backup is the copy of the file as it was. A file the upgrade added has none: Added is set, and the original state is that the file did not exist.
	Backup string `json:"backup,omitempty"` //portlint:allow emptydrop PiG-only backup manifest read back by Go; an added file has no copy
	Added  bool   `json:"added,omitempty"`  //portlint:allow emptydrop PiG-only backup manifest read back by Go
}

// backup copies each file the plan rewrites into a directory under the configuration root, with a manifest that maps the copies back.
func (u *extensionUpgrader) backup(config subprocess.ExtConfig, result *upgrade.Result) (string, error) {
	created := u.now().UTC()
	name := backupNameUnsafe.ReplaceAllString(config.Name, "_")
	dir, err := newBackupDir(filepath.Join(u.configRoot, "state", "extension-upgrade"), created.Format("20060102T150405Z")+"-"+name)
	if err != nil {
		return "", err
	}
	manifest := backupManifest{Extension: config.Name, Source: config.Source, Created: created.Format(time.RFC3339)}
	for _, change := range result.Files {
		if change.Created {
			manifest.Files = append(manifest.Files, backupFile{Path: change.Path, Added: true})
			continue
		}
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

// printUpgradeReport writes one extension's outcome: the diff of each file the plan changes, what the author still has to change, with the file, line and form of each, and the result of the rebuild.
func printUpgradeReport(w io.Writer, report extensionUpgradeReport, showDiff bool) {
	_, _ = fmt.Fprintf(w, "%s: %s\n", report.Name, report.Status)
	if report.Note != "" {
		_, _ = fmt.Fprintf(w, "  %s\n", report.Note)
	}
	for _, message := range report.Unexpected {
		_, _ = fmt.Fprintf(w, "  build error: %s\n", message)
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
		switch {
		case change.Created:
			_, _ = fmt.Fprintf(w, "  %s: new file with the helpers the changes call\n", change.Path)
		default:
			_, _ = fmt.Fprintf(w, "  %s: %d change(s) for %s\n", change.Path, len(change.Rewrites), strings.Join(symbols, ", "))
		}
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
	printManualChanges(w, result)
	switch {
	case report.Status == upgradeUpgraded && report.Built:
		_, _ = fmt.Fprintln(w, "  rebuilt: ok")
	case report.Status == upgradeManual && report.Built:
		_, _ = fmt.Fprintln(w, "  rebuilt: ok")
	case report.Status == upgradePartial:
		_, _ = fmt.Fprintln(w, "  rebuilt: no; the extension builds once the changes listed above are made")
	case report.Status == upgradePreview:
		_, _ = fmt.Fprintln(w, "  build of the rewritten extension: ok")
	case report.Status == upgradePreviewPartial:
		_, _ = fmt.Fprintln(w, "  build of the rewritten extension: fails only on the changes listed above")
	}
}

// printManualChanges lists what the upgrade leaves for the author: each use with its file and line and the form to write, then the changes the compiler still rejects and any compile error that is not an SDK change.
func printManualChanges(w io.Writer, result *upgrade.Result) {
	for _, skipped := range result.Skipped {
		_, _ = fmt.Fprintf(w, "  %s:%d: edit by hand (%s): %s\n", skipped.File, skipped.Line, skipped.Symbol, skipped.Reason)
		switch {
		case skipped.Suggest != "":
			_, _ = fmt.Fprintf(w, "    write: %s\n", skipped.Suggest)
		default:
			if rule, ok := upgrade.RuleByID(skipped.Rule); ok {
				_, _ = fmt.Fprintf(w, "    %s\n", rule.Remedy)
			}
		}
	}
	listed := map[string]int{}
	for _, skipped := range result.Skipped {
		listed[skipped.Symbol]++
	}
	for _, drift := range result.Remaining {
		// A use listed above, with its line, is not listed twice.
		if listed[drift.Symbol] >= drift.Count {
			continue
		}
		_, _ = fmt.Fprintf(w, "  still to change by hand: %s\n", drift)
		if rule, ok := upgrade.RuleByID(drift.Rule); ok {
			_, _ = fmt.Fprintf(w, "    %s:%d: %s\n", drift.File, drift.Line, rule.Remedy)
		}
	}
	for _, message := range result.Errors {
		_, _ = fmt.Fprintf(w, "  compile error: %s\n", message)
	}
}
