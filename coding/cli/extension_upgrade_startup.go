package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// pig additive (D109): see extension_upgrade_command.go.

// startupUpgradeOptions selects how startup answers extensions written for an older SDK.
type startupUpgradeOptions struct {
	// Interactive asks the user; otherwise only Auto upgrades.
	Interactive bool
	// Auto upgrades without asking: the extensionsAutoUpgrade setting.
	Auto     bool
	Upgrader *extensionUpgrader
	In       io.Reader
	Out      io.Writer
}

// upgradeDriftedExtensions answers the extensions that failed to build for an older SDK. An interactive start lists them and asks; with extensionsAutoUpgrade it upgrades without asking and prints one line each; otherwise it does nothing and the failure is reported as it always was. It returns true when every one of them was upgraded and rebuilt, so startup can load them. Startup asks once: an extension that still fails after the upgrade ends startup as it always has.
func upgradeDriftedExtensions(ctx context.Context, items []extensionDrift, opts startupUpgradeOptions) bool {
	if len(items) == 0 {
		return false
	}
	verbose := false
	if !opts.Auto {
		if !opts.Interactive {
			return false
		}
		_, _ = fmt.Fprint(opts.Out, sdkDriftNotice(items))
		_, _ = fmt.Fprint(opts.Out, "Run "+extensionUpgradeCommand(items)+" now? [y/N] ")
		answer, _ := bufio.NewReader(opts.In).ReadString('\n')
		if reply := strings.ToLower(strings.TrimSpace(answer)); reply != "y" && reply != "yes" {
			return false
		}
		verbose = true
	}
	allOk := true
	for _, item := range items {
		report := opts.Upgrader.upgrade(ctx, item.Config, false)
		if verbose {
			printUpgradeReport(opts.Out, report, true)
		} else {
			_, _ = fmt.Fprintln(opts.Out, upgradeSummaryLine(report))
		}
		allOk = allOk && report.Ok()
	}
	return allOk
}

// upgradeSummaryLine is the one line an automatic upgrade prints for an extension.
func upgradeSummaryLine(report extensionUpgradeReport) string {
	prefix := codingagent.AppName + ": "
	switch {
	case report.Status == upgradeSkipped:
		return fmt.Sprintf("%sextension %q skipped: %s", prefix, report.Name, report.Note)
	case report.Status == upgradePreview:
		return fmt.Sprintf("%sextension %q would be upgraded", prefix, report.Name)
	case report.Status == upgradePreviewPartial:
		return fmt.Sprintf("%sextension %q would be partly upgraded: %d change(s) apply, %d use(s) need a manual edit; run %s --dry-run to list them", prefix, report.Name, rewriteCount(report.Result), manualUses(report.Result), extensionUpgradeCommand([]extensionDrift{{Source: report.Source}}))
	case report.Status == upgradePartial:
		return fmt.Sprintf("%spartly upgraded extension %q: %d change(s) applied (the original files are in %s), %d use(s) need a manual edit; run %s to list them", prefix, report.Name, rewriteCount(report.Result), report.Backup, manualUses(report.Result), extensionUpgradeCommand([]extensionDrift{{Source: report.Source}}))
	case report.Ok() && report.Status == upgradeUpToDate:
		return fmt.Sprintf("%sextension %q needs no upgrade", prefix, report.Name)
	case report.Ok():
		changes := rewriteCount(report.Result)
		return fmt.Sprintf("%supgraded extension %q for the current SDK (%d changes; the original files are in %s)", prefix, report.Name, changes, report.Backup)
	}
	note := report.Note
	if note == "" && report.Result != nil && len(report.Result.Remaining) > 0 {
		note = fmt.Sprintf("%d SDK change(s) need a manual edit; run %s", len(report.Result.Remaining), extensionUpgradeCommand([]extensionDrift{{Source: report.Source}}))
	}
	if report.Backup != "" {
		note += " (the original files are in " + report.Backup + ")"
	}
	return fmt.Sprintf("%scould not upgrade extension %q: %s", prefix, report.Name, strings.TrimSpace(note))
}

// rewriteCount is the number of changes a plan applies.
func rewriteCount(result *upgrade.Result) int {
	changes := 0
	if result != nil {
		for _, file := range result.Files {
			changes += len(file.Rewrites)
		}
	}
	return changes
}

// manualUses is the number of uses a plan leaves for the author: the uses of the changes the compiler still rejects, or else the uses it listed.
func manualUses(result *upgrade.Result) int {
	if result == nil {
		return 0
	}
	uses := 0
	for _, drift := range result.Remaining {
		uses += drift.Count
	}
	return max(uses, len(result.Skipped)) + len(result.Errors)
}

// startupUpgrade answers the drifted extensions of a starting process on its terminal.
func startupUpgrade(ctx context.Context, items []extensionDrift, interactive, auto bool) bool {
	return upgradeDriftedExtensions(ctx, items, startupUpgradeOptions{
		Interactive: interactive, Auto: auto,
		Upgrader: newExtensionUpgrader(codingagent.ConfigRoot()), In: os.Stdin, Out: os.Stderr,
	})
}

// upgradeExtensionsAfterUpdate runs the extension upgrade rules of the pig that `pig update` just installed, when the extensionsAutoUpgrade setting is on. This process holds the previous rules and SDK, so the new binary does the work. A failure is a warning: the update itself succeeded.
func upgradeExtensionsAfterUpdate(enabled bool, run func() error) {
	if !enabled {
		return
	}
	if err := run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%swarning: the automatic extension upgrade after the update failed: %v\n", codingagent.AppName+": ", err)
	}
}

// runUpdatedExtensionUpgrade starts the installed pig to upgrade every Go extension, printing one line each.
func runUpdatedExtensionUpgrade() error {
	path, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(path, "extension", "upgrade", "--all", "--summary")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, os.Stderr, os.Stderr
	return cmd.Run()
}
