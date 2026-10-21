package sessiongraph

import (
	"fmt"
	"path/filepath"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func resolvedReport(paths sdk.ResolvedPaths) map[string]any {
	skills := []map[string]any{}
	for _, skill := range paths.Skills {
		skills = append(skills, map[string]any{"file": filepath.Base(skill.Path), "enabled": skill.Enabled, "source": skill.Metadata.Source, "scope": skill.Metadata.Scope, "origin": skill.Metadata.Origin})
	}
	return map[string]any{"extensions": len(paths.Extensions), "skills": skills, "prompts": len(paths.Prompts), "themes": len(paths.Themes)}
}

func packagesScript(ctx sdk.Context, args map[string]any) map[string]any {
	root, cwd, agentDir := fmt.Sprint(args["root"]), fmt.Sprint(args["pmCwd"]), fmt.Sprint(args["pmAgentDir"])
	pkg := filepath.Join(root, "pkgs", "localpkg")
	r := map[string]any{}
	settings := ok(sdk.SettingsManagerCreate(ctx, cwd, &agentDir, nil))
	pm := ok(sdk.NewDefaultPackageManager(ctx, sdk.PackageManagerOptions{Cwd: cwd, AgentDir: agentDir, SettingsManager: settings}))
	r["initial"] = map[string]any{"list": len(ok(pm.ListConfiguredPackages())), "installed": orNil(ok(pm.GetInstalledPath(pkg, "user")))}
	added, again := ok(pm.AddSourceToSettings(pkg, false)), ok(pm.AddSourceToSettings(pkg, false))
	r["added"] = map[string]any{"first": added, "again": again, "list": ok(pm.ListConfiguredPackages()), "packages": ok(settings.GetPackages()), "installed": orNil(ok(pm.GetInstalledPath(pkg, "user")))}
	r["resolved"] = resolvedReport(ok(pm.Resolve(nil)))

	events := []string{}
	check(pm.SetProgressCallback(func(event sdk.ProgressEvent) { events = append(events, event.Type+":"+event.Action) }))
	check(pm.InstallAndPersist(pkg, true))
	check(pm.Remove(pkg, true))
	removeOnly := map[string]any{"list": len(ok(pm.ListConfiguredPackages()))}
	r["removeOnly"] = removeOnly
	r["installed"] = map[string]any{"list": ok(pm.ListConfiguredPackages()), "events": events}
	r["removed"] = map[string]any{"project": ok(pm.RemoveAndPersist(pkg, true)), "list": len(ok(pm.ListConfiguredPackages())), "settings": ok(pm.RemoveSourceFromSettings(pkg, false)), "settingsAgain": ok(pm.RemoveSourceFromSettings(pkg, false))}

	check(pm.SetProgressCallback(nil))
	r["temporary"] = resolvedReport(ok(pm.ResolveExtensionSources([]string{pkg}, false, true)))
	r["updates"] = len(ok(pm.CheckForAvailableUpdates()))

	missing := []string{}
	ok(pm.AddSourceToSettings("npm:not-installed-pkg", false))
	skipped := ok(pm.Resolve(func(source string) sdk.MissingSourceAction {
		missing = append(missing, source)
		return "skip"
	}))
	r["missing"] = map[string]any{"calls": missing, "skills": len(skipped.Skills)}
	return r
}
