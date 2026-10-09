package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// pig additive (D109): `pig extension upgrade` rewrites Go extensions written for an older SDK. Pi loads extension source in process and has no counterpart.

const extensionUpgradeUsage = `Usage:
  pig extension upgrade [<name|path>...] [--all] [--dry-run] [--summary]

Rewrite Go extensions written for an older SDK so they build against this one.
Every file it changes is copied to the configuration root first, the diff is
printed, and the extension is rebuilt. A change that has no safe mechanical
form is listed for you to make.

With no argument, or with --all, it upgrades every Go extension that loads from
your settings, user and trusted project directories, and packages.

Options:
  --all        Upgrade every discovered Go extension
  --dry-run    Print the diff; change nothing
  --summary    Print one line per extension instead of the diff
`

// runExtensionUpgrade is `pig extension upgrade`.
func runExtensionUpgrade(args []string) int {
	var targets []string
	all, dryRun, summary := false, false, false
	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			fmt.Print(extensionUpgradeUsage)
			return 0
		case "--all":
			all = true
		case "--dry-run":
			dryRun = true
		case "--summary":
			summary = true
		default:
			if strings.HasPrefix(arg, "-") {
				_, _ = fmt.Fprintf(os.Stderr, "Unknown option %q.\n%s", arg, extensionUpgradeUsage)
				return 1
			}
			targets = append(targets, arg)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	configs, err := extensionUpgradeTargets(targets, all || len(targets) == 0)
	if err != nil {
		printCLIError("%v", err)
		return 1
	}
	configRoot := codingagent.ConfigRoot()
	stageExtensionSDKsAtStartup(ctx, configRoot, os.Stderr, startupSDKLockTimeout)
	return upgradeExtensions(ctx, os.Stdout, newExtensionUpgrader(configRoot), configs, dryRun, summary)
}

// upgradeExtensions upgrades each extension, prints its report, and returns the exit status: 0 when every extension builds against the current SDK, 1 otherwise.
func upgradeExtensions(ctx context.Context, w io.Writer, upgrader *extensionUpgrader, configs []subprocess.ExtConfig, dryRun, summary bool) int {
	status := 0
	for _, config := range configs {
		if ctx.Err() != nil {
			return 1
		}
		report := upgrader.upgrade(ctx, config, dryRun)
		if summary {
			_, _ = fmt.Fprintln(w, upgradeSummaryLine(report))
		} else {
			printUpgradeReport(w, report, true)
		}
		if report.Status == upgradeSkipped {
			continue
		}
		if !report.Ok() && report.Status != upgradePreview {
			status = 1
		}
	}
	if len(configs) == 0 {
		_, _ = fmt.Fprintln(w, "No Go extensions to upgrade.")
	}
	return status
}

// extensionUpgradeTargets resolves the arguments to extension configurations: a directory is the extension in it, anything else is the name of a discovered extension.
func extensionUpgradeTargets(targets []string, all bool) ([]subprocess.ExtConfig, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	var configs []subprocess.ExtConfig
	var discovered []subprocess.ExtConfig
	discover := func() []subprocess.ExtConfig {
		if discovered == nil {
			discovered = discoverExtensionConfigs(cwd)
		}
		return discovered
	}
	for _, target := range targets {
		if info, statErr := os.Stat(target); statErr == nil && info.IsDir() {
			absolute, _ := filepath.Abs(target)
			found := pathToExtConfigs(absolute, newStartupExtensionSourceResolver(nil).Resolve)
			if len(found) == 0 || found[0].ResolveError() != nil {
				return nil, fmt.Errorf("%s is not an extension: %w", target, extensionResolveError(found))
			}
			configs = append(configs, found...)
			continue
		}
		matched := false
		for _, config := range discover() {
			if config.Name == target {
				configs = append(configs, config)
				matched = true
			}
		}
		if !matched {
			return nil, fmt.Errorf("no extension named %q is loaded from your settings; pass its directory", target)
		}
	}
	if all {
		configs = append(configs, discover()...)
	}
	return uniqueGoSourceConfigs(configs), nil
}

func extensionResolveError(configs []subprocess.ExtConfig) error {
	if len(configs) == 0 {
		return fmt.Errorf("no extension found")
	}
	return configs[0].ResolveError()
}

// discoverExtensionConfigs lists the extensions a session starting in cwd loads without prompting: user scope, and project scope when the project is already trusted.
func discoverExtensionConfigs(cwd string) []subprocess.ExtConfig {
	agentDir := codingagent.AgentDir()
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	scopes := []string{"user"}
	if decision, err := codingagent.NewProjectTrustStore(agentDir).Get(cwd); err == nil && decision != nil && *decision {
		scopes = append(scopes, "workspace")
	}
	return collectExtensionConfigs(cwd, agentDir, settings, Args{}, &scopes, newStartupExtensionSourceResolver(nil).Resolve)
}

// uniqueGoSourceConfigs keeps the first configuration of each source directory.
func uniqueGoSourceConfigs(configs []subprocess.ExtConfig) []subprocess.ExtConfig {
	seen := map[string]bool{}
	var out []subprocess.ExtConfig
	for _, config := range configs {
		key := config.Source
		if key == "" {
			key = config.Path
		}
		key = canonicalPath(key)
		if seen[key] || !isGoSourceExtension(config) {
			continue
		}
		seen[key] = true
		out = append(out, config)
	}
	return out
}
