package codingagent

// pi: packages/coding-agent/src/core/settings-defaults.ts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// settingsDefaultsCase is one leaf of SETTINGS_DEFAULTS: the Pi getter expression that reads it on an empty settings manager and the PiG getter.
type settingsDefaultsCase struct {
	path string
	pi   string
	pig  func(*SettingsManager) any
}

func jsonValue(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Every leaf of Pi's SETTINGS_DEFAULTS, the runtime defaults "shared by settings accessors and the published JSON Schema", is what Pi's accessor returns on
// an empty settings manager and what PiG's accessor returns. The leaves come from Pi's module, so a default Pi adds, removes or changes fails here.
func TestSettingsDefaultsMatchPiOnAnEmptySettingsManager(t *testing.T) {
	t.Setenv("PI_CLEAR_ON_SHRINK", "")
	t.Setenv("PI_TELEMETRY", "")
	if err := os.Unsetenv("PI_TELEMETRY"); err != nil {
		t.Fatal(err)
	}
	compaction := func(sm *SettingsManager) CompactionConfig {
		config, err := sm.GetCompactionSettings()
		if err != nil {
			t.Fatal(err)
		}
		return config
	}
	cases := []settingsDefaultsCase{
		{"transport", `s.getTransport()`, func(sm *SettingsManager) any { return sm.GetTransport() }},
		{"sessionShareDestination", `s.getSessionShareDestination()`, func(sm *SettingsManager) any { return sm.GetSessionShareDestination() }},
		{"steeringMode", `s.getSteeringMode()`, func(sm *SettingsManager) any { return sm.GetSteeringMode() }},
		{"followUpMode", `s.getFollowUpMode()`, func(sm *SettingsManager) any { return sm.GetFollowUpMode() }},
		{"compaction.enabled", `s.getCompactionEnabled()`, func(sm *SettingsManager) any { return sm.GetCompactionEnabled() }},
		{"compaction.reserveTokens", `s.getCompactionSettings().reserveTokens`, func(sm *SettingsManager) any { return compaction(sm).ReserveTokens }},
		{"compaction.keepRecentTokens", `s.getCompactionSettings().keepRecentTokens`, func(sm *SettingsManager) any { return compaction(sm).KeepRecentTokens }},
		{"branchSummary.reserveTokens", `s.getBranchSummarySettings().reserveTokens`, func(sm *SettingsManager) any { return sm.GetBranchSummarySettings().ReserveTokens }},
		{"branchSummary.skipPrompt", `s.getBranchSummarySettings().skipPrompt`, func(sm *SettingsManager) any { return sm.GetBranchSummarySettings().SkipPrompt }},
		{"retry.enabled", `s.getRetrySettings().enabled`, func(sm *SettingsManager) any { return sm.GetRetrySettings().Enabled }},
		{"retry.maxRetries", `s.getRetrySettings().maxRetries`, func(sm *SettingsManager) any { return sm.GetRetrySettings().MaxRetries }},
		{"retry.baseDelayMs", `s.getRetrySettings().baseDelayMs`, func(sm *SettingsManager) any { return sm.GetRetrySettings().BaseDelayMs }},
		{"retry.maxAgentDelayMs", `s.getRetrySettings().maxAgentDelayMs`, func(sm *SettingsManager) any { return sm.GetRetrySettings().MaxAgentDelayMs }},
		{"retry.provider.maxRetryDelayMs", `s.getProviderRetrySettings().maxRetryDelayMs`, func(sm *SettingsManager) any { return sm.GetProviderRetrySettings().MaxRetryDelayMs }},
		{"hideThinkingBlock", `s.getHideThinkingBlock()`, func(sm *SettingsManager) any { return sm.GetHideThinkingBlock() }},
		{"showCacheMissNotices", `s.getShowCacheMissNotices()`, func(sm *SettingsManager) any { return sm.GetShowCacheMissNotices() }},
		{"quietStartup", `s.getQuietStartup()`, func(sm *SettingsManager) any {
			switch sm.GetQuietStartup() {
			case QuietStartupTrue:
				return true
			case QuietStartupHeader:
				return "header"
			}
			return false
		}},
		{"defaultProjectTrust", `s.getDefaultProjectTrust()`, func(sm *SettingsManager) any { return sm.GetDefaultProjectTrust() }},
		{"collapseChangelog", `s.getCollapseChangelog()`, func(sm *SettingsManager) any { return sm.GetCollapseChangelog() }},
		{"enableInstallTelemetry", `s.getEnableInstallTelemetry()`, func(sm *SettingsManager) any { return sm.GetEnableInstallTelemetry() }},
		{"enableAnalytics", `s.getEnableAnalytics()`, func(sm *SettingsManager) any { return sm.GetEnableAnalytics() }},
		{"enableSkillCommands", `s.getEnableSkillCommands()`, func(sm *SettingsManager) any { return sm.GetEnableSkillCommands() }},
		{"terminal.showImages", `s.getShowImages()`, func(sm *SettingsManager) any { return sm.GetShowImages() }},
		{"terminal.imageWidthCells", `s.getImageWidthCells()`, func(sm *SettingsManager) any { return sm.GetImageWidthCells() }},
		{"terminal.clearOnShrink", `s.getClearOnShrink()`, func(sm *SettingsManager) any { return sm.GetClearOnShrink() }},
		{"terminal.showTerminalProgress", `s.getShowTerminalProgress()`, func(sm *SettingsManager) any { return sm.GetShowTerminalProgress() }},
		{"images.autoResize", `s.getImageAutoResize()`, func(sm *SettingsManager) any { return sm.GetImageAutoResize() }},
		{"images.blockImages", `s.getBlockImages()`, func(sm *SettingsManager) any { return sm.GetBlockImages() }},
		{"doubleEscapeAction", `s.getDoubleEscapeAction()`, func(sm *SettingsManager) any { return sm.GetDoubleEscapeAction() }},
		{"treeFilterMode", `s.getTreeFilterMode()`, func(sm *SettingsManager) any { return sm.GetTreeFilterMode() }},
		{"editorPaddingX", `s.getEditorPaddingX()`, func(sm *SettingsManager) any { return sm.GetEditorPaddingX() }},
		{"outputPad", `s.getOutputPad()`, func(sm *SettingsManager) any { return sm.GetOutputPad() }},
		{"autocompleteMaxVisible", `s.getAutocompleteMaxVisible()`, func(sm *SettingsManager) any { return sm.GetAutocompleteMaxVisible() }},
		{"markdown.codeBlockIndent", `s.getCodeBlockIndent()`, func(sm *SettingsManager) any { return sm.GetCodeBlockIndent() }},
		{"markdown.mermaid", `s.getMermaidRenderingMode()`, func(sm *SettingsManager) any { return sm.GetMermaidRenderingMode() }},
		{"warnings.anthropicExtraUsage", `s.getWarnings().anthropicExtraUsage !== false`, func(sm *SettingsManager) any { return sm.GetWarnings().AnthropicExtraUsage }},
		{"cacheWarming", `s.getCacheWarmingMode()`, func(sm *SettingsManager) any { return sm.GetCacheWarmingMode() }},
		{"tuiMode", `s.getTuiMode()`, func(sm *SettingsManager) any { return sm.GetTuiMode() }},
		{"fullscreenExitOutput", `s.getFullscreenExitOutput()`, func(sm *SettingsManager) any { return sm.GetFullscreenExitOutput() }},
		{"fullscreenScrollbar", `s.getFullscreenScrollbar()`, func(sm *SettingsManager) any { return sm.GetFullscreenScrollbar() }},
		{"fullscreenCopyOnSelect", `s.getFullscreenCopyOnSelect()`, func(sm *SettingsManager) any { return sm.GetFullscreenCopyOnSelect() }},
		{"fullscreenWheelScrollLines", `s.getFullscreenWheelScrollLines()`, func(sm *SettingsManager) any {
			if lines := sm.GetFullscreenWheelScrollLines(); !lines.Auto {
				return lines.Lines
			}
			return "auto"
		}},
	}
	type piRow struct {
		Getter  any `json:"getter"`
		Default any `json:"default"`
	}
	var pi struct {
		Leaves []string          `json:"leaves"`
		Rows   map[string]piRow  `json:"rows"`
		Errors map[string]string `json:"errors"`
	}
	exprs := map[string]string{}
	for _, c := range cases {
		exprs[c.path] = c.pi
	}
	pioracle.Run(t, `
const { SettingsManager } = await load("pi-coding-agent/core/settings-manager.js");
const { SETTINGS_DEFAULTS } = await load("pi-coding-agent/core/settings-defaults.js");
const fs = await import("node:fs");
const os = await import("node:os");
const path = await import("node:path");
const root = fs.mkdtempSync(path.join(os.tmpdir(), "settingsd-"));
const agentDir = path.join(root, "agent");
const cwd = path.join(root, "proj");
fs.mkdirSync(agentDir); fs.mkdirSync(cwd);
const s = SettingsManager.create(cwd, agentDir, { projectTrusted: true });
const leaves = [];
const walk = (value, prefix) => {
	for (const [key, v] of Object.entries(value)) {
		if (v !== null && typeof v === "object") walk(v, prefix + key + ".");
		else leaves.push(prefix + key);
	}
};
walk(SETTINGS_DEFAULTS, "");
const at = (p) => p.split(".").reduce((v, k) => v[k], SETTINGS_DEFAULTS);
const rows = {}, errors = {};
for (const [p, expr] of Object.entries(input)) {
	try { rows[p] = { getter: new Function("s", "return " + expr)(s) ?? null, default: at(p) }; } catch (e) { errors[p] = String(e); }
}
fs.rmSync(root, { recursive: true, force: true });
emit({ leaves, rows, errors });`, exprs, &pi, "PI_CLEAR_ON_SHRINK=", "PI_TELEMETRY=")
	for path, message := range pi.Errors {
		t.Errorf("Pi getter for %s failed: %s", path, message)
	}
	root := t.TempDir()
	agentDir, cwd := filepath.Join(root, "agent"), filepath.Join(root, "proj")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	sm := NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
	var covered []string
	for _, c := range cases {
		covered = append(covered, c.path)
		row, ok := pi.Rows[c.path]
		if !ok {
			continue
		}
		if !reflect.DeepEqual(row.Getter, row.Default) {
			t.Errorf("%s: Pi's getter %v is not Pi's SETTINGS_DEFAULTS value %v", c.path, row.Getter, row.Default)
		}
		if got := jsonValue(t, c.pig(sm)); !reflect.DeepEqual(got, row.Default) {
			t.Errorf("%s: PiG default %v (%T), Pi %v (%T)", c.path, got, got, row.Default, row.Default)
		}
	}
	// codemode.mode and codemode.inlineBudget are read by the codemode extension; its test compares them.
	var elsewhere = []string{"codemode.inlineBudget", "codemode.mode"}
	want := slices.Clone(pi.Leaves)
	slices.Sort(want)
	have := append(slices.Clone(covered), elsewhere...)
	slices.Sort(have)
	if !slices.Equal(want, have) {
		t.Errorf("SETTINGS_DEFAULTS leaves\n Pi    %v\n tests %v\na new Pi default needs a PiG accessor and a row", want, have)
	}
}
