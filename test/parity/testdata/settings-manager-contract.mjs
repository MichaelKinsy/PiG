import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

if (process.argv[2] === "pig") {
  const output = execFileSync("go", ["test", "./internal/codingagent", "-run", "^TestSettingsManagerFirstHalfContract$", "-count=1", "-v"], { encoding: "utf8" });
  const rows = output.split("\n").filter(line => line.startsWith("SETTINGS_CONTRACT "));
  assert.equal(rows.length, 1, "one complete settings snapshot");
  console.log(rows[0]);
} else {
  assert.equal(process.argv[2], "pi");
  const root = process.env.PI_PACKAGE_ROOT ?? resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
  assert.equal(JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version, "1.0.3");
  const { SettingsManager } = await import(pathToFileURL(join(root, "dist/core/settings-manager.js")));
  const settings = SettingsManager.inMemory({ theme: "light/dark", extensions: [], defaultProjectTrust: "always" });
  settings.setDefaultThinkingLevel("high");
  settings.setCacheWarmingMode("off");
  await settings.flush();
  settings.applyOverrides({ theme: "transient" });
  await settings.reload();
  settings.setProjectTrusted(false);
  let writeError;
  try { settings.setProjectPackages(["npm:blocked"]); } catch (error) { writeError = error; }
  assert.ok(writeError);
  settings.setProjectTrusted(true);
  settings.setProjectPackages(["npm:project"]);
  // The public project setters delegate to this generic updater, matching Go's UpdateProject path.
  settings.updateProjectSettings("defaultProjectTrust", value => { value.defaultProjectTrust = "never"; });
  await settings.flush();
  const themePresence = manager => {
    const value = manager.getThemeSetting();
    return [value !== undefined, value ?? null];
  };
  const result = {
    global: settings.getGlobalSettings(), project: settings.getProjectSettings(),
    themePresence: { omitted: themePresence(SettingsManager.inMemory()), empty: themePresence(SettingsManager.inMemory({ theme: "" })) },
    themeSetting: settings.getThemeSetting(), fixedThemeConfigured: settings.getTheme() !== undefined,
    defaultTrust: settings.getDefaultProjectTrust(), untrustedWriteError: writeError.message,
  };
  const canonical = value => Array.isArray(value) ? value.map(canonical) : value && typeof value === "object" ? Object.fromEntries(Object.keys(value).sort().map(key => [key, canonical(value[key])])) : value;
  console.log("SETTINGS_CONTRACT " + JSON.stringify(canonical(result)));
}
