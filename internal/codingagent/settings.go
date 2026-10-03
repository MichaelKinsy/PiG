package codingagent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/jsonparse"
	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
	"github.com/MichaelKinsy/PiG/internal/resolvepath"
	"github.com/MichaelKinsy/PiG/internal/text"
	"github.com/MichaelKinsy/PiG/tui"
)

// PackageSource mirrors upstream settings-manager.ts PackageSource.
// JSON accepts either a bare string source or an object with resource filters and optional autoload selection.
type PackageSource struct {
	Source     string   `json:"source,omitempty"`
	Autoload   *bool    `json:"autoload,omitempty"`
	Extensions []string `json:"extensions,omitempty"`
	Skills     []string `json:"skills,omitempty"`
	Prompts    []string `json:"prompts,omitempty"`
	Themes     []string `json:"themes,omitempty"`

	// WasObject tracks whether the JSON was an object (vs bare string).
	// Upstream considers ANY object-format package as "filtered"
	// (typeof pkg === "object"), even if no filter fields are set.
	WasObject bool `json:"-"`
}

// Filtered reports whether the package was specified in object form.
// Upstream: `filtered: typeof pkg === "object"`: any object form is filtered.
// For programmatically constructed sources, filter fields also signal filtering.
func (p PackageSource) Filtered() bool {
	return p.WasObject || p.Autoload != nil || p.Extensions != nil || p.Skills != nil || p.Prompts != nil || p.Themes != nil
}

// MarshalJSON preserves upstream's string-or-object wire shape.
func (p PackageSource) MarshalJSON() ([]byte, error) {
	if !p.Filtered() {
		return json.Marshal(p.Source)
	}
	obj := map[string]any{"source": p.Source}
	if p.Autoload != nil {
		obj["autoload"] = *p.Autoload
	}
	if p.Extensions != nil {
		obj["extensions"] = p.Extensions
	}
	if p.Skills != nil {
		obj["skills"] = p.Skills
	}
	if p.Prompts != nil {
		obj["prompts"] = p.Prompts
	}
	if p.Themes != nil {
		obj["themes"] = p.Themes
	}
	return json.Marshal(obj)
}

// UnmarshalJSON preserves upstream's string-or-object wire shape.
func (p *PackageSource) UnmarshalJSON(data []byte) error {
	var source string
	if err := json.Unmarshal(data, &source); err == nil {
		*p = PackageSource{Source: source}
		return nil
	}
	type packageSourceObject PackageSource
	var obj packageSourceObject
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	*p = PackageSource(obj)
	p.WasObject = true
	return nil
}

// ─── Compaction settings types ───────────────────────────────────────────────

// CompactionConfig mirrors compaction.CompactionSettings but lives here to
// avoid an import cycle (compaction imports codingagent for SessionEntry).
type CompactionConfig struct {
	Enabled          bool
	ReserveTokens    int
	KeepRecentTokens int
}

// defaultCompactionConfig mirrors DEFAULT_COMPACTION_SETTINGS (compaction.ts).
var defaultCompactionConfig = CompactionConfig{
	Enabled:          true,
	ReserveTokens:    16384,
	KeepRecentTokens: 20000,
}

// BranchSummaryConfig mirrors compaction.BranchSummarySettings but lives here
// to avoid an import cycle.
type BranchSummaryConfig struct {
	ReserveTokens    int  `json:"reserveTokens,omitempty"`
	SkipPrompt       bool `json:"skipPrompt,omitempty"`
	reserveTokensSet bool `json:"-"`
	skipPromptSet    bool `json:"-"`
}

type branchSummaryWire struct {
	ReserveTokens *int  `json:"reserveTokens,omitempty"`
	SkipPrompt    *bool `json:"skipPrompt,omitempty"`
}

// CompactionSettingsJSON mirrors upstream Settings.compaction JSON shape
// (settings-manager.ts:74). Pointer to *bool for enabled to distinguish
// absent vs. explicitly-false.
type CompactionSettingsJSON struct {
	Enabled *bool `json:"enabled,omitempty"`
	// ReserveTokens and KeepRecentTokens retain JavaScript numbers until getter validation, including zero, fractions, and non-finite runtime overrides.
	ReserveTokens    *float64 `json:"reserveTokens,omitempty"`
	KeepRecentTokens *float64 `json:"keepRecentTokens,omitempty"`
	// ModelOverrides maps exact "provider/modelId" keys to token settings
	// that take precedence for that model.
	ModelOverrides map[string]CompactionModelOverride `json:"modelOverrides,omitempty"`
	extra          map[string]json.RawMessage
}

// CompactionModelOverride mirrors upstream CompactionModelOverride. Pointers
// keep an explicit zero, as upstream's `override ?? ordinary` does.
type CompactionModelOverride struct {
	ReserveTokens    *float64 `json:"reserveTokens,omitempty"`
	KeepRecentTokens *float64 `json:"keepRecentTokens,omitempty"`
	extra            map[string]json.RawMessage
	invalidEntry     json.RawMessage
}

// ProviderRetrySettings mirrors upstream retry.provider settings.
// Used for provider/SDK-level request retries and deadlines.
type ProviderRetrySettings struct {
	TimeoutMs  *int `json:"timeoutMs,omitempty"`
	MaxRetries *int `json:"maxRetries,omitempty"`
	// MaxRetryDelayMs is a pointer so an explicit 0 (upstream disables the cap:
	// provider-retry.ts "set it to zero to disable the limit") is distinguishable
	// from unset (nil -> 60s default). A plain int cannot tell 0 from absent.
	MaxRetryDelayMs *int `json:"maxRetryDelayMs,omitempty"`
}

// RetrySettingsJSON mirrors upstream Settings.retry JSON shape
// (settings-manager.ts:682-690). Defaults: enabled=true, maxRetries=3,
// baseDelayMs=2000.
type RetrySettingsJSON struct {
	Enabled *bool `json:"enabled,omitempty"`
	// MaxRetries and BaseDelayMs are pointers so an explicit 0 is kept, as
	// upstream's `?? default` does.
	MaxRetries  *int `json:"maxRetries,omitempty"`
	BaseDelayMs *int `json:"baseDelayMs,omitempty"`
	// MaxAgentDelayMs caps each agent-level retry delay (default 60000).
	MaxAgentDelayMs *int                   `json:"maxAgentDelayMs,omitempty"`
	Provider        *ProviderRetrySettings `json:"provider,omitempty"`
}

// ThinkingBudgetsSettings mirrors upstream ThinkingBudgetsSettings.
// Custom token budgets for each thinking level.
// Mirrors upstream settings-manager.ts ThinkingBudgetsSettings.
type ThinkingBudgetsSettings struct {
	Minimal *int `json:"minimal,omitempty"`
	Low     *int `json:"low,omitempty"`
	Medium  *int `json:"medium,omitempty"`
	High    *int `json:"high,omitempty"`
}

// MarkdownSettings mirrors upstream MarkdownSettings.
// Mirrors upstream settings-manager.ts MarkdownSettings.
type MarkdownSettings struct {
	CodeBlockIndent string `json:"codeBlockIndent,omitempty"`
	Mermaid         string `json:"mermaid,omitempty"`
}

// WarningSettings mirrors upstream warning settings.
type WarningSettings struct {
	AnthropicExtraUsage    bool `json:"anthropicExtraUsage,omitempty"`
	anthropicExtraUsageSet bool `json:"-"`
}

func (w WarningSettings) MarshalJSON() ([]byte, error) {
	type warningSettingsWire struct {
		AnthropicExtraUsage *bool `json:"anthropicExtraUsage,omitempty"`
	}
	wire := warningSettingsWire{}
	if w.anthropicExtraUsageSet || w.AnthropicExtraUsage {
		wire.AnthropicExtraUsage = new(w.AnthropicExtraUsage)
	}
	return json.Marshal(wire)
}

func (w *WarningSettings) UnmarshalJSON(data []byte) error {
	type warningSettingsWire struct {
		AnthropicExtraUsage *bool `json:"anthropicExtraUsage,omitempty"`
	}
	var wire warningSettingsWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*w = WarningSettings{}
	if wire.AnthropicExtraUsage != nil {
		w.AnthropicExtraUsage = *wire.AnthropicExtraUsage
		w.anthropicExtraUsageSet = true
	}
	return nil
}

// terminalSettingsWire mirrors upstream terminal nested settings.
type terminalSettingsWire struct {
	ShowImages           *bool `json:"showImages,omitempty"`
	ImageWidthCells      int   `json:"imageWidthCells,omitempty"`
	ClearOnShrink        *bool `json:"clearOnShrink,omitempty"`
	ShowTerminalProgress *bool `json:"showTerminalProgress,omitempty"`
	// Hyperlinks, Images, and TrueColor keep the authored JSON value; see
	// Settings.GetTerminalCapabilityOverrides.
	Hyperlinks json.RawMessage `json:"hyperlinks,omitempty"`
	Images     json.RawMessage `json:"images,omitempty"`
	TrueColor  json.RawMessage `json:"trueColor,omitempty"`
}

// imageSettingsWire mirrors upstream images nested settings.
type imageSettingsWire struct {
	AutoResize  *bool `json:"autoResize,omitempty"`
	BlockImages *bool `json:"blockImages,omitempty"`
}

// settingsWire mirrors upstream settings JSON while tolerating legacy pig
// flattened fields during unmarshal.
type settingsWire struct {
	LastChangelogVersion   string `json:"lastChangelogVersion,omitempty"`
	DefaultProvider        string `json:"defaultProvider,omitempty"`
	DefaultModel           string `json:"defaultModel,omitempty"`
	DefaultThinkingLevel   string `json:"defaultThinkingLevel,omitempty"`
	Transport              string `json:"transport,omitempty"`
	SteeringMode           string `json:"steeringMode,omitempty"`
	FollowUpMode           string `json:"followUpMode,omitempty"`
	TuiMode                string `json:"tuiMode,omitempty"`
	FullscreenExitOutput   string `json:"fullscreenExitOutput,omitempty"`
	FullscreenScrollbar    string `json:"fullscreenScrollbar,omitempty"`
	FullscreenCopyOnSelect *bool  `json:"fullscreenCopyOnSelect,omitempty"`
	// FullscreenWheelScrollLines keeps the authored JSON value, including null.
	FullscreenWheelScrollLines json.RawMessage         `json:"fullscreenWheelScrollLines,omitempty"`
	DeviceID                   string                  `json:"deviceId,omitempty"`
	Codemode                   *CodemodeSettings       `json:"codemode,omitempty"`
	MaskSecretInput            *bool                   `json:"maskSecretInput,omitempty"`
	Theme                      *string                 `json:"theme,omitempty"`
	Compaction                 *CompactionSettingsJSON `json:"compaction,omitempty"`
	BranchSummary              *branchSummaryWire      `json:"branchSummary,omitempty"`
	Retry                      *RetrySettingsJSON      `json:"retry,omitempty"`
	HideThinkingBlock          *bool                   `json:"hideThinkingBlock,omitempty"`
	ShellPath                  string                  `json:"shellPath,omitempty"`
	// QuietStartup keeps the authored JSON value, including null, which replaces a lower layer's value as in upstream deepMergeSettings.
	QuietStartup           json.RawMessage          `json:"quietStartup,omitempty"`
	ShellCommandPrefix     string                   `json:"shellCommandPrefix,omitempty"`
	LegacyCommandPrefix    string                   `json:"commandPrefix,omitempty"`
	NpmCommand             []string                 `json:"npmCommand,omitzero"`
	CollapseChangelog      *bool                    `json:"collapseChangelog,omitempty"`
	ShowCacheMissNotices   *bool                    `json:"showCacheMissNotices,omitempty"`
	EnableInstallTelemetry *bool                    `json:"enableInstallTelemetry,omitempty"`
	EnableAnalytics        *bool                    `json:"enableAnalytics,omitempty"`
	TrackingID             string                   `json:"trackingId,omitempty"`
	Packages               []PackageSource          `json:"packages,omitzero"`
	Extensions             []string                 `json:"extensions,omitzero"`
	Skills                 []string                 `json:"skills,omitzero"`
	Prompts                []string                 `json:"prompts,omitzero"`
	Themes                 []string                 `json:"themes,omitzero"`
	EnableSkillCommands    *bool                    `json:"enableSkillCommands,omitempty"`
	Terminal               *terminalSettingsWire    `json:"terminal,omitempty"`
	Images                 *imageSettingsWire       `json:"images,omitempty"`
	LegacyShowImages       *bool                    `json:"showImages,omitempty"`
	LegacyImageWidthCells  int                      `json:"imageWidthCells,omitempty"`
	LegacyClearOnShrink    *bool                    `json:"clearOnShrink,omitempty"`
	LegacyImageAutoResize  *bool                    `json:"imageAutoResize,omitempty"`
	LegacyBlockImages      *bool                    `json:"blockImages,omitempty"`
	EnabledModels          []string                 `json:"enabledModels,omitzero"`
	DefaultTools           []string                 `json:"defaultTools,omitzero"`
	ModelThinkingLevels    map[string]string        `json:"modelThinkingLevels,omitempty"`
	DoubleEscapeAction     string                   `json:"doubleEscapeAction,omitempty"`
	TreeFilterMode         string                   `json:"treeFilterMode,omitempty"`
	DefaultProjectTrust    string                   `json:"defaultProjectTrust,omitempty"`
	ThinkingBudgets        *ThinkingBudgetsSettings `json:"thinkingBudgets,omitempty"`
	EditorPaddingX         *int                     `json:"editorPaddingX,omitempty"`
	OutputPad              *int                     `json:"outputPad,omitempty"`
	ExternalEditor         string                   `json:"externalEditor,omitempty"`
	AutocompleteMaxVisible *int                     `json:"autocompleteMaxVisible,omitempty"`
	ShowHardwareCursor     *bool                    `json:"showHardwareCursor,omitempty"`
	Markdown               *MarkdownSettings        `json:"markdown,omitempty"`
	Warnings               *WarningSettings         `json:"warnings,omitempty"`
	SessionDir             string                   `json:"sessionDir,omitempty"`
	// HTTPIdleTimeoutMs keeps the raw JSON value: upstream accepts numbers,
	// numeric strings and "disabled".
	HTTPProxy                 string           `json:"httpProxy,omitempty"`
	WebSocketConnectTimeoutMs any              `json:"websocketConnectTimeoutMs,omitempty"`
	HTTPIdleTimeoutMs         any              `json:"httpIdleTimeoutMs,omitempty"`
	CacheWarming              CacheWarmingMode `json:"cacheWarming,omitempty"`
}

const defaultHTTPIdleTimeoutMs = 300_000

// WheelScrollLines is the number of lines a wheel event scrolls, or Auto for velocity-based acceleration (upstream `WheelScrollLines = number | "auto"`). Lines is 1 to 100 when read from settings.
type WheelScrollLines struct {
	Auto  bool
	Lines float64
}

// CodemodeMode is how the codemode tool presents tools while it is active. Mirrors upstream CodemodeMode (settings-manager.ts:101).
type CodemodeMode string

const (
	// CodemodeModeOn appends the codemode declaration to each callable tool's description and lists only tools without direct exposure in the codemode description.
	CodemodeModeOn CodemodeMode = "on"
	// CodemodeModeOnly lists every callable tool in the codemode description and does not declare active direct tools to the model.
	CodemodeModeOnly CodemodeMode = "only"
)

// CodemodeSettings mirrors upstream CodemodeSettings (settings-manager.ts:103).
type CodemodeSettings struct {
	// Mode defaults to on.
	Mode CodemodeMode `json:"mode,omitempty"`
	// InlineBudget is the estimated tokens (characters / 4) the codemode description may spend on tool declarations. Default 3000.
	InlineBudget *int `json:"inlineBudget,omitempty"`
}

// CacheWarmingMode selects when prompt caches are kept warm. Mirrors upstream
// settings-manager.ts CacheWarmingMode.
type CacheWarmingMode string

// CacheWarmingModes lists the modes in Pi's order (CACHE_WARMING_MODES).
var CacheWarmingModes = []CacheWarmingMode{"off", "streaming", "idle"}

// defaultCacheWarmingMode is Pi's default: warm only while the agent runs.
const defaultCacheWarmingMode CacheWarmingMode = "streaming"

func parseHTTPIdleTimeoutMs(value any) (int, bool) {
	switch v := value.(type) {
	case nil:
		return 0, false
	case int:
		if v < 0 {
			return 0, false
		}
		return v, true
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return 0, false
		}
		return int(math.Floor(v)), true
	case string:
		trimmed := strings.TrimSpace(v)
		if strings.EqualFold(trimmed, "disabled") {
			return 0, true
		}
		if trimmed == "" {
			return 0, false
		}
		parsed, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return 0, false
		}
		return parseHTTPIdleTimeoutMs(parsed)
	default:
		return 0, false
	}
}

// QuietStartup mirrors upstream QuietStartup (settings-manager.ts:111-112), the union boolean | "header": true hides all startup output, "header" keeps only the startup header.
type QuietStartup uint8

const (
	// QuietStartupFalse is false: the startup header and details show. It is the default.
	QuietStartupFalse QuietStartup = iota
	// QuietStartupTrue is true: the startup header and details are hidden.
	QuietStartupTrue
	// QuietStartupHeader is "header": the startup header shows and the details are hidden.
	QuietStartupHeader
)

// String returns the value as upstream's String(quietStartup) spells it: "false", "true" or "header".
func (q QuietStartup) String() string {
	switch q {
	case QuietStartupTrue:
		return "true"
	case QuietStartupHeader:
		return "header"
	default:
		return "false"
	}
}

// MarshalJSON writes the upstream JSON value: false, true or "header".
func (q QuietStartup) MarshalJSON() ([]byte, error) {
	if q == QuietStartupHeader {
		return []byte(`"header"`), nil
	}
	return []byte(q.String()), nil
}

// UnmarshalJSON reads a settings value as upstream getQuietStartup does (settings-manager.ts:1089-1092): true and "header" keep their meaning and every other value is false.
func (q *QuietStartup) UnmarshalJSON(data []byte) error {
	switch strings.TrimSpace(string(data)) {
	case "true":
		*q = QuietStartupTrue
	case `"header"`:
		*q = QuietStartupHeader
	default:
		*q = QuietStartupFalse
	}
	return nil
}

// ─── Settings ─────────────────────────────────────────────────────────────────

// Settings mirrors upstream Settings interface.
type Settings struct {
	DefaultProvider      string `json:"defaultProvider,omitempty"`
	DefaultModel         string `json:"defaultModel,omitempty"`
	DefaultThinkingLevel string `json:"defaultThinkingLevel,omitempty"`
	// HideThinkingBlock mirrors upstream settings.hideThinkingBlock
	// (settings-manager.ts:77). When true, thinking blocks show as
	// "Thinking..." stubs instead of the full reasoning trace. Toggled
	// by Ctrl+T; persisted so the preference survives sessions.
	HideThinkingBlock    bool `json:"hideThinkingBlock,omitempty"`
	hideThinkingBlockSet bool `json:"-"`
	// Theme is the configured name. JSON and SettingsManager.SetTheme preserve explicitly empty values.
	Theme      string `json:"theme,omitempty"`
	themeEmpty bool   `json:"-"`
	// themeNonString records a theme key whose JSON value is not a string, such as null. It replaces a lower layer's theme and reads as unset.
	themeNonString bool   `json:"-"`
	ShellPath      string `json:"shellPath,omitempty"`
	// CommandPrefix prepended to every bash tool invocation as a
	// separate first line (e.g. "set -e\n" or "export PATH=...\n").
	CommandPrefix                  string          `json:"commandPrefix,omitempty"`
	QuietStartup                   QuietStartup    `json:"quietStartup,omitempty"`
	quietStartupSet                bool            `json:"-"`
	Packages                       []PackageSource `json:"packages,omitzero"`
	Extensions                     []string        `json:"extensions,omitempty"`
	Skills                         []string        `json:"skills,omitempty"`
	Prompts                        []string        `json:"prompts,omitempty"`
	Themes                         []string        `json:"themes,omitempty"`
	SessionDir                     string          `json:"sessionDir,omitempty"`
	HTTPProxy                      string          `json:"httpProxy,omitempty"`
	WebSocketConnectTimeoutMs      *int            `json:"websocketConnectTimeoutMs,omitempty"`
	webSocketConnectTimeoutInvalid any
	webSocketConnectTimeoutNull    bool
	HTTPIdleTimeoutMs              *int     `json:"httpIdleTimeoutMs,omitempty"`
	EnabledModels                  []string `json:"enabledModels,omitempty"`
	// DefaultTools is the initial built-in tool selection; nil means the
	// upstream default read, bash, edit and write.
	DefaultTools []string `json:"defaultTools,omitempty"`
	// ModelThinkingLevels holds per-model default thinking levels keyed by
	// "provider/modelId".
	ModelThinkingLevels map[string]string `json:"modelThinkingLevels,omitempty"`

	// httpIdleTimeoutInvalid holds a present httpIdleTimeoutMs value that
	// does not parse; GetHttpIdleTimeoutMs reports it as an error.
	httpIdleTimeoutInvalid any

	// CacheWarming is read from global settings only, because each refresh
	// costs money. Mirrors upstream settings.cacheWarming.
	CacheWarming CacheWarmingMode `json:"cacheWarming,omitempty"`

	// LastChangelogVersion records the binary version at which the
	// user last saw the startup changelog notification. Used to gate
	// "what's new" display to only new entries since last run.
	// Mirrors upstream settings.lastChangelogVersion (settings-manager.ts:66).
	LastChangelogVersion string `json:"lastChangelogVersion,omitempty"`

	// Compaction mirrors upstream settings.compaction (settings-manager.ts:74).
	// Nested to match upstream JSON schema:
	// { "compaction": { "enabled": true, "reserveTokens": 16384, "keepRecentTokens": 20000 } }
	Compaction *CompactionSettingsJSON `json:"compaction,omitempty"`

	// BranchSummary mirrors upstream settings.branchSummary
	// (settings-manager.ts:75). Nested to match upstream JSON schema:
	// { "branchSummary": { "skipPrompt": true, "reserveTokens": 16384 } }
	BranchSummary *BranchSummaryConfig `json:"branchSummary,omitempty"`

	// DoubleEscapeAction controls what double-Esc with empty editor does.
	// Values: "fork", "tree", "none". Default: "tree".
	// Mirrors upstream settings.doubleEscapeAction (settings-manager.ts:93).
	DoubleEscapeAction string `json:"doubleEscapeAction,omitempty"`

	// TreeFilterMode controls the default /tree filter.
	// Values: "default", "no-tools", "user-only", "labeled-only", "all".
	// Default: "default".
	// Mirrors upstream settings.treeFilterMode (settings-manager.ts:94).
	TreeFilterMode string `json:"treeFilterMode,omitempty"`

	// DefaultProjectTrust is the fallback when no extension or saved decision
	// resolves project trust. Values: "ask" | "always" | "never". Default: "ask".
	// Global setting only. Mirrors upstream settings.defaultProjectTrust
	// (settings-manager.ts:94).
	DefaultProjectTrust string `json:"defaultProjectTrust,omitempty"`

	// SteeringMode controls queue dispatch when multiple messages are queued.
	// Values: "all" | "one-at-a-time". Default: "one-at-a-time".
	// Mirrors upstream settings.steeringMode (settings-manager.ts:71).
	SteeringMode string `json:"steeringMode,omitempty"`

	// FollowUpMode controls how queued follow-up messages are dispatched.
	// Values: "all" | "one-at-a-time". Default: "one-at-a-time".
	// Mirrors upstream settings.followUpMode (settings-manager.ts:72).
	FollowUpMode string `json:"followUpMode,omitempty"`

	// TuiMode selects the regular or fullscreen terminal layout. Default: fullscreen.
	TuiMode string `json:"tuiMode,omitempty"`

	// FullscreenExitOutput controls whether fullscreen exit prints the transcript
	// or restores the previous screen. Values: transcript, resume-hint. Default: transcript.
	FullscreenExitOutput string `json:"fullscreenExitOutput,omitempty"`

	// FullscreenScrollbar controls fullscreen scrollbars. Values: auto, always,
	// hidden. Default: auto; it has no effect in regular mode.
	FullscreenScrollbar string `json:"fullscreenScrollbar,omitempty"`

	// FullscreenCopyOnSelect controls automatic clipboard copy when a fullscreen
	// text selection completes. Default: true; it has no effect in regular mode.
	FullscreenCopyOnSelect *bool `json:"fullscreenCopyOnSelect,omitempty"`

	// FullscreenWheelScrollLines holds the authored `fullscreenWheelScrollLines` JSON value, a line count or "auto". GetFullscreenWheelScrollLines validates it. A present JSON null is kept because it overrides a lower layer.
	FullscreenWheelScrollLines json.RawMessage `json:"fullscreenWheelScrollLines,omitempty"`

	// DeviceID is the stable UUID of this installation. Only the global layer is read for it: GetOrCreateDeviceID ignores project settings.
	// Mirrors upstream settings.deviceId (settings-manager.ts:156).
	DeviceID string `json:"deviceId,omitempty"`

	// Codemode configures how the codemode tool presents tools. Mirrors upstream settings.codemode (settings-manager.ts:176).
	Codemode *CodemodeSettings `json:"codemode,omitempty"`

	// MaskSecretInput controls configurable login-input privacy. Nil means true; false restores Pi's plain-text prompts.
	MaskSecretInput *bool `json:"maskSecretInput,omitempty"`

	// CollapseChangelog shows condensed startup update notices. The /changelog command always shows every released entry.
	CollapseChangelog    bool `json:"collapseChangelog,omitempty"`
	collapseChangelogSet bool `json:"-"`

	// ShowCacheMissNotices controls transcript notices for significant prompt
	// cache misses. Default: false. Mirrors upstream settings.showCacheMissNotices
	// (settings-manager.ts:96).
	ShowCacheMissNotices    bool `json:"showCacheMissNotices,omitempty"`
	showCacheMissNoticesSet bool `json:"-"`

	// EnableSkillCommands, when false, hides skill commands from the
	// /skill:name autocomplete popup. Default: true.
	// Mirrors upstream settings.enableSkillCommands (settings-manager.ts:89).
	// Note: this is a *bool so false can explicitly disable (vs absent=true default).
	// Use GetEnableSkillCommands() instead of reading directly.
	EnableSkillCommands *bool `json:"enableSkillCommands,omitempty"`

	// EnableInstallTelemetry controls whether package-install telemetry may be sent
	// by pig-specific install telemetry hooks when they are configured.
	// Default: true.
	// Mirrors upstream settings.enableInstallTelemetry (settings-manager.ts), but
	// the transport itself is a separate implementation concern.
	EnableInstallTelemetry *bool `json:"enableInstallTelemetry,omitempty"`

	// EnableAnalytics is the opt-in analytics data sharing setting (default
	// false). Mirrors upstream settings plumbing only: nothing sends data.
	EnableAnalytics *bool `json:"enableAnalytics,omitempty"`
	// TrackingID is the analytics tracking identifier generated on the first
	// opt-in. Bug reports strip it.
	TrackingID string `json:"trackingId,omitempty"`

	// Retry mirrors upstream settings.retry (settings-manager.ts:682-690).
	// Nested JSON: { "retry": { "enabled": true, "maxRetries": 2,
	//   "baseDelayMs": 10000, "maxDelayMs": 60000 } }
	Retry *RetrySettingsJSON `json:"retry,omitempty"`

	// ShowImages controls whether inline images are rendered in tool results
	// and assistant messages. Default: true.
	// Mirrors upstream settings.showImages (settings-manager.ts:84).
	ShowImages *bool `json:"showImages,omitempty"`

	// ImageWidthCells is the max width (in terminal columns) for inline images.
	// Default: 60. Mirrors upstream settings.imageWidthCells (settings-manager.ts:85).
	ImageWidthCells int `json:"imageWidthCells,omitempty"`

	// BlockImages, when true, blocks image rendering entirely. Default: false.
	// Mirrors upstream settings.blockImages (settings-manager.ts:86).
	BlockImages    bool `json:"blockImages,omitempty"`
	blockImagesSet bool `json:"-"`

	// ImageAutoResize, when true, automatically resizes images to fit the
	// terminal width. Default: true.
	// Mirrors upstream settings.imageAutoResize (settings-manager.ts:87).
	ImageAutoResize *bool `json:"imageAutoResize,omitempty"`

	// Transport controls HTTP transport behavior.
	// Values: "sse" | "websocket" | "websocket-cached" | "auto". Default: "auto".
	// Mirrors upstream settings.transport (settings-manager.ts:70).
	Transport string `json:"transport,omitempty"`

	// ShowHardwareCursor controls whether the hardware cursor is shown.
	// Default: false (or PI_HARDWARE_CURSOR=1 env).
	// Mirrors upstream settings.showHardwareCursor (settings-manager.ts:944).
	ShowHardwareCursor *bool `json:"showHardwareCursor,omitempty"`

	// EditorPaddingX is horizontal padding (in cells) for the editor.
	// Range: 0-3. Default: 0.
	// Mirrors upstream settings.editorPaddingX (settings-manager.ts:951).
	EditorPaddingX *int `json:"editorPaddingX,omitempty"`

	// OutputPad is horizontal padding (in cells) for chat output.
	// Range: 0-1. Default: 1.
	// Mirrors upstream settings.outputPad (settings-manager.ts:116).
	OutputPad *int `json:"outputPad,omitempty"`

	// ExternalEditor is the command Ctrl+G uses before VISUAL/EDITOR fallbacks.
	// Mirrors upstream settings.externalEditor (settings-manager.ts:93).
	ExternalEditor string `json:"externalEditor,omitempty"`

	// AutocompleteMaxVisible is the max number of autocomplete suggestions shown.
	// Range: 3-20. Default: 5.
	// Mirrors upstream settings.autocompleteMaxVisible (settings-manager.ts:958).
	AutocompleteMaxVisible *int `json:"autocompleteMaxVisible,omitempty"`

	// ClearOnShrink controls whether the screen is cleared when terminal shrinks.
	// Default: false.
	// Mirrors upstream settings.clearOnShrink (settings-manager.ts:891).
	ClearOnShrink *bool `json:"clearOnShrink,omitempty"`

	// ShowTerminalProgress controls OSC 9;4 terminal progress indicators.
	// Default: false.
	ShowTerminalProgress *bool `json:"showTerminalProgress,omitempty"`

	// terminalHyperlinks, terminalImages, and terminalTrueColor hold the
	// authored terminal.hyperlinks, terminal.images, and terminal.trueColor
	// JSON values. Upstream types them boolean | "auto",
	// "kitty" | "iterm2" | "auto" | false, and boolean | "auto", keeps whatever
	// the file holds, and lets getTerminalCapabilityOverrides ignore the rest.
	terminalHyperlinks json.RawMessage
	terminalImages     json.RawMessage
	terminalTrueColor  json.RawMessage

	// ThinkingBudgets controls per-level token budgets for extended thinking.
	// Mirrors upstream settings.thinkingBudgets (settings-manager.ts:857).
	ThinkingBudgets *ThinkingBudgetsSettings `json:"thinkingBudgets,omitempty"`

	// NpmCommand overrides the npm command used for package operations.
	// Default: ["npm"].
	// Mirrors upstream settings.npmCommand (settings-manager.ts:830).
	NpmCommand []string `json:"npmCommand,omitempty"`

	// Markdown holds markdown rendering settings.
	// Mirrors upstream settings.markdown (settings-manager.ts:965).
	Markdown *MarkdownSettings `json:"markdown,omitempty"`

	// Warnings holds persisted warning-dismissal preferences.
	Warnings *WarningSettings `json:"warnings,omitempty"`
}

func cloneIntPtr(v *int) *int {
	if v == nil {
		return nil
	}
	x := *v
	return &x
}

func cloneBoolPtr(v *bool) *bool {
	if v == nil {
		return nil
	}
	x := *v
	return &x
}

func cloneFloatPtr(v *float64) *float64 {
	if v == nil {
		return nil
	}
	return new(*v)
}

func cloneCompactionSettings(v *CompactionSettingsJSON) *CompactionSettingsJSON {
	if v == nil {
		return nil
	}
	out := &CompactionSettingsJSON{
		Enabled:          cloneBoolPtr(v.Enabled),
		extra:            maps.Clone(v.extra),
		ReserveTokens:    cloneFloatPtr(v.ReserveTokens),
		KeepRecentTokens: cloneFloatPtr(v.KeepRecentTokens),
	}
	if v.ModelOverrides != nil {
		out.ModelOverrides = make(map[string]CompactionModelOverride, len(v.ModelOverrides))
		for key, override := range v.ModelOverrides {
			out.ModelOverrides[key] = CompactionModelOverride{
				extra: maps.Clone(override.extra), invalidEntry: bytes.Clone(override.invalidEntry),
				ReserveTokens:    cloneFloatPtr(override.ReserveTokens),
				KeepRecentTokens: cloneFloatPtr(override.KeepRecentTokens),
			}
		}
	}
	return out
}

// mergeCompactionModelOverrides deep-merges project model overrides into
// merged, field by field, as upstream deepMergeSettings merges nested objects.
func mergeCompactionModelOverrides(merged *CompactionSettingsJSON, project map[string]CompactionModelOverride) {
	if len(project) == 0 {
		return
	}
	if merged.ModelOverrides == nil {
		merged.ModelOverrides = make(map[string]CompactionModelOverride, len(project))
	}
	for key, override := range project {
		current := merged.ModelOverrides[key]
		if override.invalidEntry != nil || current.invalidEntry != nil {
			current = CompactionModelOverride{invalidEntry: bytes.Clone(override.invalidEntry)}
		}
		if current.extra == nil {
			current.extra = make(map[string]json.RawMessage)
		}
		maps.Copy(current.extra, override.extra)
		mergeCompactionNumber(&current.ReserveTokens, current.extra, "reserveTokens", override.ReserveTokens, override.extra)
		mergeCompactionNumber(&current.KeepRecentTokens, current.extra, "keepRecentTokens", override.KeepRecentTokens, override.extra)
		merged.ModelOverrides[key] = current
	}
}

func cloneProviderRetrySettings(v *ProviderRetrySettings) *ProviderRetrySettings {
	if v == nil {
		return nil
	}
	out := &ProviderRetrySettings{
		TimeoutMs:  cloneIntPtr(v.TimeoutMs),
		MaxRetries: cloneIntPtr(v.MaxRetries),
	}
	if v.MaxRetryDelayMs != nil {
		d := *v.MaxRetryDelayMs
		out.MaxRetryDelayMs = &d
	}
	return out
}

func cloneRetrySettings(v *RetrySettingsJSON) *RetrySettingsJSON {
	if v == nil {
		return nil
	}
	return &RetrySettingsJSON{
		Enabled:         cloneBoolPtr(v.Enabled),
		MaxRetries:      cloneIntPtr(v.MaxRetries),
		BaseDelayMs:     cloneIntPtr(v.BaseDelayMs),
		MaxAgentDelayMs: cloneIntPtr(v.MaxAgentDelayMs),
		Provider:        cloneProviderRetrySettings(v.Provider),
	}
}

func cloneBranchSummary(v *BranchSummaryConfig) *BranchSummaryConfig {
	if v == nil {
		return nil
	}
	return &BranchSummaryConfig{
		ReserveTokens:    v.ReserveTokens,
		SkipPrompt:       v.SkipPrompt,
		reserveTokensSet: v.reserveTokensSet,
		skipPromptSet:    v.skipPromptSet,
	}
}

func cloneThinkingBudgets(v *ThinkingBudgetsSettings) *ThinkingBudgetsSettings {
	if v == nil {
		return nil
	}
	return &ThinkingBudgetsSettings{
		Minimal: cloneIntPtr(v.Minimal),
		Low:     cloneIntPtr(v.Low),
		Medium:  cloneIntPtr(v.Medium),
		High:    cloneIntPtr(v.High),
	}
}

func cloneMarkdownSettings(v *MarkdownSettings) *MarkdownSettings {
	if v == nil {
		return nil
	}
	return &MarkdownSettings{CodeBlockIndent: v.CodeBlockIndent, Mermaid: v.Mermaid}
}

func cloneWarningSettings(v *WarningSettings) *WarningSettings {
	if v == nil {
		return nil
	}
	return &WarningSettings{AnthropicExtraUsage: v.AnthropicExtraUsage, anthropicExtraUsageSet: v.anthropicExtraUsageSet}
}

func cloneCodemodeSettings(v *CodemodeSettings) *CodemodeSettings {
	if v == nil {
		return nil
	}
	return &CodemodeSettings{Mode: v.Mode, InlineBudget: cloneIntPtr(v.InlineBudget)}
}

// mergeCodemodeSettings merges the nested codemode object field by field, project over global (upstream deepMergeObjects).
func mergeCodemodeSettings(global, project *CodemodeSettings) *CodemodeSettings {
	if project == nil {
		return global
	}
	merged := cloneCodemodeSettings(global)
	if merged == nil {
		merged = &CodemodeSettings{}
	}
	if project.Mode != "" {
		merged.Mode = project.Mode
	}
	if project.InlineBudget != nil {
		merged.InlineBudget = cloneIntPtr(project.InlineBudget)
	}
	return merged
}

func clonePackageSources(src []PackageSource) []PackageSource {
	if src == nil {
		return nil
	}
	out := make([]PackageSource, len(src))
	for i, pkg := range src {
		out[i] = PackageSource{
			Source:     pkg.Source,
			Autoload:   cloneBoolPtr(pkg.Autoload),
			Extensions: slices.Clone(pkg.Extensions),
			Skills:     slices.Clone(pkg.Skills),
			Prompts:    slices.Clone(pkg.Prompts),
			Themes:     slices.Clone(pkg.Themes),
			WasObject:  pkg.WasObject,
		}
	}
	return out
}

func cloneSettings(s Settings) Settings {
	return Settings{
		DefaultProvider:                s.DefaultProvider,
		DefaultModel:                   s.DefaultModel,
		DefaultThinkingLevel:           s.DefaultThinkingLevel,
		HideThinkingBlock:              s.HideThinkingBlock,
		hideThinkingBlockSet:           s.hideThinkingBlockSet,
		Theme:                          s.Theme,
		themeEmpty:                     s.themeEmpty,
		themeNonString:                 s.themeNonString,
		ShellPath:                      s.ShellPath,
		CommandPrefix:                  s.CommandPrefix,
		QuietStartup:                   s.QuietStartup,
		quietStartupSet:                s.quietStartupSet,
		Packages:                       clonePackageSources(s.Packages),
		Extensions:                     slices.Clone(s.Extensions),
		Skills:                         slices.Clone(s.Skills),
		Prompts:                        slices.Clone(s.Prompts),
		Themes:                         slices.Clone(s.Themes),
		SessionDir:                     s.SessionDir,
		HTTPProxy:                      s.HTTPProxy,
		WebSocketConnectTimeoutMs:      cloneIntPtr(s.WebSocketConnectTimeoutMs),
		webSocketConnectTimeoutNull:    s.webSocketConnectTimeoutNull,
		webSocketConnectTimeoutInvalid: s.webSocketConnectTimeoutInvalid,
		HTTPIdleTimeoutMs:              cloneIntPtr(s.HTTPIdleTimeoutMs),
		httpIdleTimeoutInvalid:         s.httpIdleTimeoutInvalid,
		CacheWarming:                   s.CacheWarming,
		EnabledModels:                  slices.Clone(s.EnabledModels),
		DefaultTools:                   slices.Clone(s.DefaultTools),
		ModelThinkingLevels:            maps.Clone(s.ModelThinkingLevels),
		LastChangelogVersion:           s.LastChangelogVersion,
		Compaction:                     cloneCompactionSettings(s.Compaction),
		BranchSummary:                  cloneBranchSummary(s.BranchSummary),
		DoubleEscapeAction:             s.DoubleEscapeAction,
		TreeFilterMode:                 s.TreeFilterMode,
		DefaultProjectTrust:            s.DefaultProjectTrust,
		SteeringMode:                   s.SteeringMode,
		FollowUpMode:                   s.FollowUpMode,
		TuiMode:                        s.TuiMode,
		FullscreenExitOutput:           s.FullscreenExitOutput,
		FullscreenScrollbar:            s.FullscreenScrollbar,
		FullscreenCopyOnSelect:         cloneBoolPtr(s.FullscreenCopyOnSelect),
		FullscreenWheelScrollLines:     slices.Clone(s.FullscreenWheelScrollLines),
		DeviceID:                       s.DeviceID,
		Codemode:                       cloneCodemodeSettings(s.Codemode),
		MaskSecretInput:                cloneBoolPtr(s.MaskSecretInput),
		CollapseChangelog:              s.CollapseChangelog,
		collapseChangelogSet:           s.collapseChangelogSet,
		ShowCacheMissNotices:           s.ShowCacheMissNotices,
		showCacheMissNoticesSet:        s.showCacheMissNoticesSet,
		EnableSkillCommands:            cloneBoolPtr(s.EnableSkillCommands),
		EnableInstallTelemetry:         cloneBoolPtr(s.EnableInstallTelemetry),
		EnableAnalytics:                cloneBoolPtr(s.EnableAnalytics),
		TrackingID:                     s.TrackingID,
		Retry:                          cloneRetrySettings(s.Retry),
		ShowImages:                     cloneBoolPtr(s.ShowImages),
		ImageWidthCells:                s.ImageWidthCells,
		BlockImages:                    s.BlockImages,
		blockImagesSet:                 s.blockImagesSet,
		ImageAutoResize:                cloneBoolPtr(s.ImageAutoResize),
		Transport:                      s.Transport,
		ShowHardwareCursor:             cloneBoolPtr(s.ShowHardwareCursor),
		EditorPaddingX:                 cloneIntPtr(s.EditorPaddingX),
		OutputPad:                      cloneIntPtr(s.OutputPad),
		ExternalEditor:                 s.ExternalEditor,
		AutocompleteMaxVisible:         cloneIntPtr(s.AutocompleteMaxVisible),
		ClearOnShrink:                  cloneBoolPtr(s.ClearOnShrink),
		ShowTerminalProgress:           cloneBoolPtr(s.ShowTerminalProgress),
		terminalHyperlinks:             slices.Clone(s.terminalHyperlinks),
		terminalImages:                 slices.Clone(s.terminalImages),
		terminalTrueColor:              slices.Clone(s.terminalTrueColor),
		ThinkingBudgets:                cloneThinkingBudgets(s.ThinkingBudgets),
		NpmCommand:                     slices.Clone(s.NpmCommand),
		Markdown:                       cloneMarkdownSettings(s.Markdown),
		Warnings:                       cloneWarningSettings(s.Warnings),
	}
}

func (s Settings) themeSetting() *string {
	if s.Theme == "" && !s.themeEmpty {
		return nil
	}
	return new(s.Theme)
}

// MarshalJSON emits upstream's settings wire shape while preserving pig's
// internal flattened representation.
func (s Settings) MarshalJSON() ([]byte, error) {
	w := settingsWire{
		LastChangelogVersion:       s.LastChangelogVersion,
		DefaultProvider:            s.DefaultProvider,
		DefaultModel:               s.DefaultModel,
		DefaultThinkingLevel:       s.DefaultThinkingLevel,
		Transport:                  s.Transport,
		SteeringMode:               s.SteeringMode,
		FollowUpMode:               s.FollowUpMode,
		TuiMode:                    s.TuiMode,
		FullscreenExitOutput:       s.FullscreenExitOutput,
		FullscreenScrollbar:        s.FullscreenScrollbar,
		FullscreenCopyOnSelect:     s.FullscreenCopyOnSelect,
		FullscreenWheelScrollLines: s.FullscreenWheelScrollLines,
		DeviceID:                   s.DeviceID,
		Codemode:                   s.Codemode,
		MaskSecretInput:            s.MaskSecretInput,
		Theme:                      s.themeSetting(),
		Compaction:                 s.Compaction,
		Retry:                      s.Retry,
		ShellPath:                  s.ShellPath,
		NpmCommand:                 s.NpmCommand,
		EnableInstallTelemetry:     s.EnableInstallTelemetry,
		EnableAnalytics:            s.EnableAnalytics,
		TrackingID:                 s.TrackingID,
		Packages:                   s.Packages,
		Extensions:                 s.Extensions,
		Skills:                     s.Skills,
		Prompts:                    s.Prompts,
		Themes:                     s.Themes,
		EnableSkillCommands:        s.EnableSkillCommands,
		EnabledModels:              s.EnabledModels,
		DefaultTools:               s.DefaultTools,
		ModelThinkingLevels:        s.ModelThinkingLevels,
		DoubleEscapeAction:         s.DoubleEscapeAction,
		TreeFilterMode:             s.TreeFilterMode,
		DefaultProjectTrust:        s.DefaultProjectTrust,
		ThinkingBudgets:            s.ThinkingBudgets,
		EditorPaddingX:             s.EditorPaddingX,
		OutputPad:                  s.OutputPad,
		ExternalEditor:             s.ExternalEditor,
		AutocompleteMaxVisible:     s.AutocompleteMaxVisible,
		ShowHardwareCursor:         s.ShowHardwareCursor,
		Markdown:                   s.Markdown,
		Warnings:                   s.Warnings,
		SessionDir:                 s.SessionDir,
		HTTPProxy:                  s.HTTPProxy,
		WebSocketConnectTimeoutMs:  webSocketConnectTimeoutWire(s),
		HTTPIdleTimeoutMs:          httpIdleTimeoutWire(s),
		CacheWarming:               s.CacheWarming,
	}
	if s.hideThinkingBlockSet {
		v := s.HideThinkingBlock
		w.HideThinkingBlock = &v
	}
	if s.quietStartupSet {
		w.QuietStartup, _ = s.QuietStartup.MarshalJSON()
	}
	if s.CommandPrefix != "" {
		w.ShellCommandPrefix = s.CommandPrefix
	}
	if s.collapseChangelogSet {
		v := s.CollapseChangelog
		w.CollapseChangelog = &v
	}
	if s.showCacheMissNoticesSet {
		v := s.ShowCacheMissNotices
		w.ShowCacheMissNotices = &v
	}
	if s.BranchSummary != nil {
		w.BranchSummary = &branchSummaryWire{}
		if s.BranchSummary.reserveTokensSet || s.BranchSummary.ReserveTokens != 0 {
			w.BranchSummary.ReserveTokens = new(s.BranchSummary.ReserveTokens)
		}
		if s.BranchSummary.skipPromptSet {
			v := s.BranchSummary.SkipPrompt
			w.BranchSummary.SkipPrompt = &v
		}
	}
	if s.ShowImages != nil || s.ImageWidthCells > 0 || s.ClearOnShrink != nil || s.ShowTerminalProgress != nil ||
		s.terminalHyperlinks != nil || s.terminalImages != nil || s.terminalTrueColor != nil {
		w.Terminal = &terminalSettingsWire{
			ShowImages:           s.ShowImages,
			ImageWidthCells:      s.ImageWidthCells,
			ClearOnShrink:        s.ClearOnShrink,
			ShowTerminalProgress: s.ShowTerminalProgress,
			Hyperlinks:           s.terminalHyperlinks,
			Images:               s.terminalImages,
			TrueColor:            s.terminalTrueColor,
		}
	}
	if s.ImageAutoResize != nil || s.blockImagesSet {
		w.Images = &imageSettingsWire{AutoResize: s.ImageAutoResize}
		if s.blockImagesSet {
			v := s.BlockImages
			w.Images.BlockImages = &v
		}
	}
	return json.Marshal(w)
}

// UnmarshalJSON accepts upstream's settings wire shape and legacy pig flat
// aliases for backward compatibility.
func (s *Settings) UnmarshalJSON(data []byte) error {
	var legacy map[string]any
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	if _, ok := legacy["steeringMode"]; !ok {
		if queueMode, ok := legacy["queueMode"]; ok {
			legacy["steeringMode"] = queueMode
		}
	}
	if _, ok := legacy["transport"]; !ok {
		if websockets, ok := legacy["websockets"].(bool); ok {
			if websockets {
				legacy["transport"] = "websocket"
			} else {
				legacy["transport"] = "sse"
			}
		}
	}
	if rawSkills, ok := legacy["skills"].(map[string]any); ok {
		if _, exists := legacy["enableSkillCommands"]; !exists {
			if enabled, ok := rawSkills["enableSkillCommands"]; ok {
				legacy["enableSkillCommands"] = enabled
			}
		}
		if dirs, ok := rawSkills["customDirectories"].([]any); ok && len(dirs) > 0 {
			legacy["skills"] = dirs
		} else {
			delete(legacy, "skills")
		}
	}
	if rawRetry, ok := legacy["retry"].(map[string]any); ok {
		provider, _ := rawRetry["provider"].(map[string]any)
		if maxDelayMs, ok := rawRetry["maxDelayMs"]; ok {
			if provider == nil {
				provider = map[string]any{}
			}
			if _, exists := provider["maxRetryDelayMs"]; !exists {
				provider["maxRetryDelayMs"] = maxDelayMs
			}
			rawRetry["provider"] = provider
			delete(rawRetry, "maxDelayMs")
		}
	}
	// Decoding drops a mistyped theme, so record its presence first.
	rawTheme, themePresent := legacy["theme"]
	_, themeIsString := rawTheme.(string)
	w, err := decodeSettingsWire(legacy)
	if err != nil {
		return err
	}
	s.LastChangelogVersion = w.LastChangelogVersion
	s.DefaultProvider = w.DefaultProvider
	s.DefaultModel = w.DefaultModel
	s.DefaultThinkingLevel = w.DefaultThinkingLevel
	s.Transport = w.Transport
	s.SteeringMode = w.SteeringMode
	s.FollowUpMode = w.FollowUpMode
	s.TuiMode = w.TuiMode
	s.FullscreenExitOutput = w.FullscreenExitOutput
	s.FullscreenScrollbar = w.FullscreenScrollbar
	s.FullscreenCopyOnSelect = w.FullscreenCopyOnSelect
	s.FullscreenWheelScrollLines = w.FullscreenWheelScrollLines
	s.DeviceID = w.DeviceID
	s.Codemode = w.Codemode
	s.MaskSecretInput = w.MaskSecretInput
	s.Theme = ""
	s.themeEmpty = w.Theme != nil && *w.Theme == ""
	s.themeNonString = themePresent && !themeIsString
	if w.Theme != nil {
		s.Theme = *w.Theme
	}
	s.Compaction = w.Compaction
	if w.BranchSummary != nil {
		s.BranchSummary = &BranchSummaryConfig{}
		if w.BranchSummary.ReserveTokens != nil {
			s.BranchSummary.ReserveTokens = *w.BranchSummary.ReserveTokens
			s.BranchSummary.reserveTokensSet = true
		}
		if w.BranchSummary.SkipPrompt != nil {
			s.BranchSummary.SkipPrompt = *w.BranchSummary.SkipPrompt
			s.BranchSummary.skipPromptSet = true
		}
	} else {
		s.BranchSummary = nil
	}
	s.Retry = w.Retry
	if w.HideThinkingBlock != nil {
		s.HideThinkingBlock = *w.HideThinkingBlock
		s.hideThinkingBlockSet = true
	}
	s.ShellPath = w.ShellPath
	if w.QuietStartup != nil {
		_ = s.QuietStartup.UnmarshalJSON(w.QuietStartup)
		s.quietStartupSet = true
	}
	s.CommandPrefix = firstNonEmpty(w.ShellCommandPrefix, w.LegacyCommandPrefix)
	if w.CollapseChangelog != nil {
		s.CollapseChangelog = *w.CollapseChangelog
		s.collapseChangelogSet = true
	}
	if w.ShowCacheMissNotices != nil {
		s.ShowCacheMissNotices = *w.ShowCacheMissNotices
		s.showCacheMissNoticesSet = true
	}
	s.EnableInstallTelemetry = w.EnableInstallTelemetry
	s.EnableAnalytics = w.EnableAnalytics
	s.TrackingID = w.TrackingID
	s.Packages = w.Packages
	s.Extensions = w.Extensions
	s.Skills = w.Skills
	s.Prompts = w.Prompts
	s.Themes = w.Themes
	s.EnableSkillCommands = w.EnableSkillCommands
	if w.Terminal != nil {
		s.ShowImages = w.Terminal.ShowImages
		s.ImageWidthCells = w.Terminal.ImageWidthCells
		s.ClearOnShrink = w.Terminal.ClearOnShrink
		s.ShowTerminalProgress = w.Terminal.ShowTerminalProgress
		s.terminalHyperlinks = w.Terminal.Hyperlinks
		s.terminalImages = w.Terminal.Images
		s.terminalTrueColor = w.Terminal.TrueColor
	}
	if s.ShowImages == nil {
		s.ShowImages = w.LegacyShowImages
	}
	if s.ImageWidthCells == 0 {
		s.ImageWidthCells = w.LegacyImageWidthCells
	}
	if s.ClearOnShrink == nil {
		s.ClearOnShrink = w.LegacyClearOnShrink
	}
	if w.Images != nil {
		s.ImageAutoResize = w.Images.AutoResize
		if w.Images.BlockImages != nil {
			s.BlockImages = *w.Images.BlockImages
			s.blockImagesSet = true
		}
	}
	if s.ImageAutoResize == nil {
		s.ImageAutoResize = w.LegacyImageAutoResize
	}
	if !s.blockImagesSet && w.LegacyBlockImages != nil {
		s.BlockImages = *w.LegacyBlockImages
		s.blockImagesSet = true
	}
	s.EnabledModels = w.EnabledModels
	s.DefaultTools = w.DefaultTools
	s.ModelThinkingLevels = w.ModelThinkingLevels
	s.DoubleEscapeAction = w.DoubleEscapeAction
	s.TreeFilterMode = w.TreeFilterMode
	s.DefaultProjectTrust = w.DefaultProjectTrust
	s.ThinkingBudgets = w.ThinkingBudgets
	s.EditorPaddingX = w.EditorPaddingX
	s.OutputPad = w.OutputPad
	s.ExternalEditor = w.ExternalEditor
	s.AutocompleteMaxVisible = w.AutocompleteMaxVisible
	s.ShowHardwareCursor = w.ShowHardwareCursor
	s.Markdown = w.Markdown
	s.Warnings = w.Warnings
	s.SessionDir = w.SessionDir
	s.HTTPProxy = w.HTTPProxy
	s.webSocketConnectTimeoutNull = false
	if value, present := legacy["websocketConnectTimeoutMs"]; present && value == nil {
		s.webSocketConnectTimeoutNull = true
	}
	s.WebSocketConnectTimeoutMs, s.webSocketConnectTimeoutInvalid = nil, nil
	if w.WebSocketConnectTimeoutMs != nil {
		if timeout, ok := parseHTTPIdleTimeoutMs(w.WebSocketConnectTimeoutMs); ok {
			s.WebSocketConnectTimeoutMs = &timeout
		} else {
			s.webSocketConnectTimeoutInvalid = w.WebSocketConnectTimeoutMs
		}
	}
	s.HTTPIdleTimeoutMs, s.httpIdleTimeoutInvalid = nil, nil
	if w.HTTPIdleTimeoutMs != nil {
		if timeoutMs, ok := parseHTTPIdleTimeoutMs(w.HTTPIdleTimeoutMs); ok {
			s.HTTPIdleTimeoutMs = &timeoutMs
		} else {
			s.httpIdleTimeoutInvalid = w.HTTPIdleTimeoutMs
		}
	}
	s.CacheWarming = w.CacheWarming
	s.NpmCommand = w.NpmCommand
	return nil
}

// decodeSettingsWire decodes the settings object into settingsWire. Upstream
// reads settings with JSON.parse, which never rejects a file for one field's
// type, so a field whose JSON type does not match is dropped and the rest of
// the file still loads.
func decodeSettingsWire(raw map[string]any) (settingsWire, error) {
	sanitized := false
	for {
		normalized, err := json.Marshal(raw)
		if err != nil {
			return settingsWire{}, err
		}
		var w settingsWire
		err = json.Unmarshal(normalized, &w)
		if err == nil {
			return w, nil
		}
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) && deleteJSONPath(raw, strings.Split(typeErr.Field, ".")) {
			continue
		}
		// The error does not name a removable path (for example, one inside
		// a custom decoder such as a package entry): drop the undecodable
		// array elements and top-level keys once, then retry.
		if sanitized || !dropUndecodableSettings(raw) {
			return w, err
		}
		sanitized = true
	}
}

// dropUndecodableSettings removes each top-level key whose value does not
// decode into settingsWire; for an array, only the elements that do not
// decode go. It reports whether it removed anything.
func dropUndecodableSettings(raw map[string]any) bool {
	decodes := func(key string, value any) bool {
		data, err := json.Marshal(map[string]any{key: value})
		if err != nil {
			return false
		}
		var w settingsWire
		return json.Unmarshal(data, &w) == nil
	}
	removed := false
	for key, value := range raw {
		if decodes(key, value) {
			continue
		}
		removed = true
		items, isArray := value.([]any)
		if !isArray {
			delete(raw, key)
			continue
		}
		kept := make([]any, 0, len(items))
		for _, item := range items {
			if decodes(key, []any{item}) {
				kept = append(kept, item)
			}
		}
		raw[key] = kept
	}
	return removed
}

// deleteJSONPath removes the deepest object key along path that exists. It
// reports whether it removed one.
func deleteJSONPath(object map[string]any, path []string) bool {
	if len(path) == 0 || path[0] == "" {
		return false
	}
	value, ok := object[path[0]]
	if !ok {
		return false
	}
	if nested, isObject := value.(map[string]any); isObject && len(path) > 1 && deleteJSONPath(nested, path[1:]) {
		return true
	}
	delete(object, path[0])
	return true
}

// GetShellPath satisfies tools.SettingsView so the bash tool can resolve the
// user's preferred shell without importing internal/codingagent (which would
// create a cycle).
func (s Settings) GetShellPath() (string, error) { return normalizeSettingsPath(s.ShellPath) }
func (s Settings) GetCommandPrefix() string      { return s.CommandPrefix }

// GetMaskSecretInput returns the login-input privacy setting, enabled by default.
func (s Settings) GetMaskSecretInput() bool {
	// pig divergence (D80): the user can restore Pi's plain-text input behavior with false.
	return s.MaskSecretInput == nil || *s.MaskSecretInput
}

// GetShowImages returns whether inline images should be rendered. Default true.
func (s Settings) GetShowImages() bool {
	if s.ShowImages == nil {
		return true
	}
	return *s.ShowImages
}

// GetImageWidthCells returns the max image width in columns. Default 60.
func (s Settings) GetImageWidthCells() int {
	if s.ImageWidthCells <= 0 {
		return 60
	}
	return s.ImageWidthCells
}

// GetImageAutoResize returns whether images should auto-resize. Default true.
func (s Settings) GetImageAutoResize() bool {
	if s.ImageAutoResize == nil {
		return true
	}
	return *s.ImageAutoResize
}

// GetShowHardwareCursor returns whether the hardware cursor is shown.
// Default: false (or PI_HARDWARE_CURSOR=1 env).
func (s Settings) GetShowHardwareCursor() bool {
	if s.ShowHardwareCursor == nil {
		return os.Getenv("PI_HARDWARE_CURSOR") == "1"
	}
	return *s.ShowHardwareCursor
}

// GetEditorPaddingX returns horizontal editor padding. Default: 0.
func (s Settings) GetEditorPaddingX() int {
	if s.EditorPaddingX == nil {
		return 0
	}
	return *s.EditorPaddingX
}

// GetOutputPad returns zero only for an explicit zero setting, and one otherwise.
func (s Settings) GetOutputPad() int {
	if s.OutputPad != nil && *s.OutputPad == 0 {
		return 0
	}
	return 1
}

// GetAutocompleteMaxVisible returns max autocomplete items. Default: 5.
func (s Settings) GetAutocompleteMaxVisible() int {
	if s.AutocompleteMaxVisible == nil {
		return 5
	}
	return *s.AutocompleteMaxVisible
}

// GetClearOnShrink returns whether screen clears on terminal shrink. Default: false.
func (s Settings) GetClearOnShrink() bool {
	if s.ClearOnShrink == nil {
		return os.Getenv("PI_CLEAR_ON_SHRINK") == "1"
	}
	return *s.ClearOnShrink
}

// GetTerminalCapabilityOverrides mirrors upstream
// SettingsManager.getTerminalCapabilityOverrides: terminal.images "kitty" or
// "iterm2" selects that protocol and false disables images; a boolean
// terminal.trueColor or terminal.hyperlinks overrides detection. "auto" and
// any other value leave detection alone.
func (s Settings) GetTerminalCapabilityOverrides() tui.CapabilityOverrides {
	var overrides tui.CapabilityOverrides
	switch images := decodeSettingJSON(s.terminalImages).(type) {
	case string:
		if images == string(tui.ImageProtocolKitty) || images == string(tui.ImageProtocolITerm2) {
			protocol := tui.ImageProtocol(images)
			overrides.Images = &protocol
		}
	case bool:
		if !images {
			none := tui.ImageProtocol("")
			overrides.Images = &none
		}
	}
	if trueColor, ok := decodeSettingJSON(s.terminalTrueColor).(bool); ok {
		overrides.TrueColor = &trueColor
	}
	if hyperlinks, ok := decodeSettingJSON(s.terminalHyperlinks).(bool); ok {
		overrides.Hyperlinks = &hyperlinks
	}
	return overrides
}

// decodeSettingJSON decodes one authored settings value, or returns nil when it
// is absent or malformed.
func decodeSettingJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	return value
}

// GetShowTerminalProgress returns whether OSC 9;4 terminal progress indicators are enabled.
func (s Settings) GetShowTerminalProgress() bool {
	if s.ShowTerminalProgress == nil {
		return false
	}
	return *s.ShowTerminalProgress
}

// GetCodeBlockIndent returns the markdown code block indent string. Default: "  ".
func (s Settings) GetCodeBlockIndent() string {
	if s.Markdown == nil || s.Markdown.CodeBlockIndent == "" {
		return "  "
	}
	return s.Markdown.CodeBlockIndent
}

// GetHideThinkingBlock returns whether thinking blocks should be hidden.
func (s Settings) GetHideThinkingBlock() bool { return s.HideThinkingBlock }

// GetShowCacheMissNotices returns whether transcript cache-miss notices are shown.
func (s Settings) GetShowCacheMissNotices() bool { return s.ShowCacheMissNotices }

// GetQuietStartup returns the quiet startup setting: true, "header" or false.
func (s Settings) GetQuietStartup() QuietStartup { return s.QuietStartup }

// GetCollapseChangelog returns whether changelog output is collapsed.
func (s Settings) GetCollapseChangelog() bool { return s.CollapseChangelog }

// GetBlockImages returns whether image rendering is blocked entirely.
func (s Settings) GetBlockImages() bool { return s.BlockImages }

// ─── SettingsManager ──────────────────────────────────────────────────────────

// SettingsManager loads and merges settings from global and project scopes.
// Merge order: global defaults → project → CLI flags.
type SettingsManager struct {
	// mu guards the settings layers: a cache-warming refresh reads the mode
	// from its own goroutine while /settings writes it.
	mu             sync.RWMutex
	global         Settings
	project        Settings
	merged         Settings
	agentDir       string
	cwd            string
	projectTrusted bool
	globalLoadErr  error
	projectLoadErr error
	errors         []SettingsError
	memory         *memorySettingsStorage
}

type memorySettingsStorage struct {
	global  Settings
	project Settings
}

// NewInMemorySettingsManager creates an independent settings manager without file I/O.
// Ports packages/coding-agent/src/core/settings-manager.ts:403-410.
func NewInMemorySettingsManager(settings Settings) *SettingsManager {
	initial := cloneSettings(settings)
	return &SettingsManager{
		global: cloneSettings(initial), merged: cloneSettings(initial), projectTrusted: true,
		memory: &memorySettingsStorage{global: initial},
	}
}

// SettingsError mirrors upstream settings-manager.ts load/write diagnostics.
type SettingsError struct {
	Scope string
	// Path is the settings file behind the error. Mirrors upstream
	// SettingsError.path, which file-backed storage always sets.
	Path  string
	Error error
}

// NewSettingsManager creates a SettingsManager for the given directories.
func NewSettingsManager(cwd, agentDir string) *SettingsManager {
	return NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
}

// NewSettingsManagerWithProjectTrust creates a settings manager that reads
// project settings only when projectTrusted is true.
func NewSettingsManagerWithProjectTrust(cwd, agentDir string, projectTrusted bool) *SettingsManager {
	sm := &SettingsManager{cwd: cwd, agentDir: agentDir, projectTrusted: projectTrusted}
	sm.Load()
	return sm
}

// DefaultAgentDir returns Pi's configured agent directory in shared mode, or <ConfigRoot>/agent otherwise.
func DefaultAgentDir() string {
	if UsePiDirs() {
		if configured := os.Getenv("PI_CODING_AGENT_DIR"); configured != "" {
			return ExpandTildePath(configured)
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".pi", "agent")
	}
	return filepath.Join(ConfigRoot(), "agent")
}

// AgentDir returns the global settings directory backing this manager.
func (sm *SettingsManager) AgentDir() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.agentDir
}

// CWD returns the project directory backing this manager.
func (sm *SettingsManager) CWD() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.cwd
}

// Load reloads the backing settings layers and discards transient overrides. Previously recorded errors remain until DrainErrors.
func (sm *SettingsManager) Load() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	// upstream: packages/coding-agent/src/core/settings-manager.ts:reload
	if sm.memory != nil {
		sm.global = cloneSettings(sm.memory.global)
		sm.project = Settings{}
		if sm.projectTrusted {
			sm.project = cloneSettings(sm.memory.project)
		}
	} else {
		globalPath := filepath.Join(sm.agentDir, "settings.json")
		if global, err := loadSettingsFile(globalPath); err == nil {
			sm.global = global
			sm.globalLoadErr = nil
		} else {
			sm.globalLoadErr = err
			if !errors.Is(err, os.ErrNotExist) {
				sm.errors = append(sm.errors, SettingsError{Scope: "global", Path: globalPath, Error: err})
			}
		}
		if !sm.projectTrusted {
			sm.project = Settings{}
			sm.projectLoadErr = nil
			sm.merged = cloneSettings(sm.global)
			return
		}
		projectPath := filepath.Join(ProjectConfigDir(sm.cwd), "settings.json")
		if project, err := loadSettingsFile(projectPath); err == nil {
			sm.project = project
			sm.projectLoadErr = nil
		} else {
			sm.projectLoadErr = err
			if !errors.Is(err, os.ErrNotExist) {
				sm.errors = append(sm.errors, SettingsError{Scope: "project", Path: projectPath, Error: err})
			}
		}
	}
	sm.merged = mergeSettings(sm.global, sm.project)
}

// Get returns the merged settings.
func (sm *SettingsManager) Get() Settings {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return cloneSettings(sm.merged)
}

// Reload refreshes the merged view from file or memory storage after earlier writes finish.
func (sm *SettingsManager) Reload() { sm.Load() }

// DrainErrors returns accumulated load/write diagnostics, or an empty list, and clears them.
func (sm *SettingsManager) DrainErrors() []SettingsError {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if len(sm.errors) == 0 {
		return []SettingsError{}
	}
	out := append([]SettingsError(nil), sm.errors...)
	sm.errors = nil
	return out
}

// GetGlobalSettings returns the persisted global settings layer.
func (sm *SettingsManager) GetGlobalSettings() Settings {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return cloneSettings(sm.global)
}

// GetProjectSettings returns the persisted project settings layer.
func (sm *SettingsManager) GetProjectSettings() Settings {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return cloneSettings(sm.project)
}

// IsProjectTrusted reports whether project settings are readable and writable.
func (sm *SettingsManager) IsProjectTrusted() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.projectTrusted
}

// SetProjectTrusted changes project-settings access without reloading global settings. Unchanged trust preserves the current merged view.
// Ports packages/coding-agent/src/core/settings-manager.ts:515-537.
func (sm *SettingsManager) SetProjectTrusted(trusted bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.projectTrusted == trusted {
		return
	}
	sm.projectTrusted = trusted
	sm.project = Settings{}
	sm.projectLoadErr = nil
	if trusted {
		if sm.memory != nil {
			sm.project = cloneSettings(sm.memory.project)
		} else {
			path := filepath.Join(ProjectConfigDir(sm.cwd), "settings.json")
			project, err := loadSettingsFile(path)
			sm.project, sm.projectLoadErr = project, err
			if err != nil {
				sm.errors = append(sm.errors, SettingsError{Scope: "project", Path: path, Error: err})
			}
		}
	}
	sm.merged = mergeSettings(sm.global, sm.project)
}

// ApplyOverrides applies non-persistent overrides on top of the merged settings.
// Mirrors upstream SettingsManager.applyOverrides.
func (sm *SettingsManager) ApplyOverrides(overrides Settings) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.merged = mergeSettings(sm.merged, overrides)
}

// ThemeOverride returns ApplyOverrides input that selects theme, as Pi's applyOverrides({theme}). An empty name is a selection, not an omitted key.
func ThemeOverride(theme string) Settings {
	return Settings{Theme: theme, themeEmpty: theme == ""}
}

// Flush waits for queued writes. pig writes settings synchronously, so this is a no-op.
// Mirrors upstream SettingsManager.flush() API for parity.
func (sm *SettingsManager) Flush() error { return nil }

// GetCompactionSettings resolves ordinary settings and rejects invalid authored token values.
func (sm *SettingsManager) GetCompactionSettings() (CompactionConfig, error) {
	return sm.GetModelCompactionSettings("", "")
}

// GetModelCompactionSettings resolves each token field through the exact model override, ordinary setting, and built-in default. Ordinary values are validated before their overrides.
func (sm *SettingsManager) GetModelCompactionSettings(provider, modelID string) (CompactionConfig, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	settings := sm.merged.Compaction
	reserve, err := getCompactionTokenSetting(settings, "reserveTokens", provider, modelID)
	if err != nil {
		return CompactionConfig{}, err
	}
	recent, err := getCompactionTokenSetting(settings, "keepRecentTokens", provider, modelID)
	if err != nil {
		return CompactionConfig{}, err
	}
	return CompactionConfig{Enabled: compactionEnabled(settings), ReserveTokens: reserve, KeepRecentTokens: recent}, nil
}

// GetCompactionEnabled returns the global compaction toggle independently of model token validation.
func (sm *SettingsManager) GetCompactionEnabled() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return compactionEnabled(sm.merged.Compaction)
}

func compactionEnabled(s *CompactionSettingsJSON) bool {
	return s == nil || s.Enabled == nil || *s.Enabled
}

// GetCompactionReserveTokens resolves and validates the reserve token setting. The optional model is a provider/model-ID pair.
func (sm *SettingsManager) GetCompactionReserveTokens(model ...string) (int, error) {
	provider, id := "", ""
	if len(model) == 2 {
		provider, id = model[0], model[1]
	}
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return getCompactionTokenSetting(sm.merged.Compaction, "reserveTokens", provider, id)
}

// GetCompactionKeepRecentTokens resolves and validates the retention setting. The optional model is a provider/model-ID pair.
func (sm *SettingsManager) GetCompactionKeepRecentTokens(model ...string) (int, error) {
	provider, id := "", ""
	if len(model) == 2 {
		provider, id = model[0], model[1]
	}
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return getCompactionTokenSetting(sm.merged.Compaction, "keepRecentTokens", provider, id)
}

// GetBranchSummarySettings returns branch summary settings, applying any
// reserve-token override from the merged settings.
// Mirrors upstream SettingsManager.getBranchSummarySettings (settings-manager.ts).
func (sm *SettingsManager) GetBranchSummarySettings() BranchSummaryConfig {
	s := sm.Get()
	result := BranchSummaryConfig{ReserveTokens: 16384}
	if s.BranchSummary != nil {
		if s.BranchSummary.reserveTokensSet || s.BranchSummary.ReserveTokens != 0 {
			result.ReserveTokens = s.BranchSummary.ReserveTokens
		}
		result.SkipPrompt = s.BranchSummary.SkipPrompt
	}
	return result
}

// GetBranchSummarySkipPrompt returns whether branch-summary prompt skipping is enabled.
func (sm *SettingsManager) GetBranchSummarySkipPrompt() bool {
	return sm.GetBranchSummarySettings().SkipPrompt
}

// GetDoubleEscapeAction returns the double-escape action setting.
// Default: "tree". Mirrors upstream SettingsManager.getDoubleEscapeAction
// (settings-manager.ts:944-946).
func (sm *SettingsManager) GetDoubleEscapeAction() string {
	a := sm.Get().DoubleEscapeAction
	if a == "" {
		return "tree"
	}
	return a
}

// GetDefaultProjectTrust returns the fallback project-trust mode.
// Values: "ask" | "always" | "never". Default: "ask".
func (sm *SettingsManager) GetDefaultProjectTrust() string {
	v := sm.GetGlobalSettings().DefaultProjectTrust
	if v == "always" || v == "never" {
		return v
	}
	return "ask"
}

// GetCacheWarmingMode returns the global cache-warming mode, or "streaming"
// when it is unset or invalid. Project settings are ignored because each
// refresh costs money. Mirrors upstream getCacheWarmingMode.
func (sm *SettingsManager) GetCacheWarmingMode() CacheWarmingMode {
	mode := sm.GetGlobalSettings().CacheWarming
	if slices.Contains(CacheWarmingModes, mode) {
		return mode
	}
	return defaultCacheWarmingMode
}

// SetCacheWarmingMode persists the cache-warming mode to global settings.
// Mirrors upstream setCacheWarmingMode.
func (sm *SettingsManager) SetCacheWarmingMode(mode CacheWarmingMode) error {
	return sm.UpdateGlobal(func(s *Settings) { s.CacheWarming = mode })
}

// GetLastChangelogVersion returns the last seen changelog version string.
// Mirrors upstream getLastChangelogVersion (settings-manager.ts:529).
func (sm *SettingsManager) GetLastChangelogVersion() string {
	return sm.Get().LastChangelogVersion
}

// SetLastChangelogVersion records that the user has seen changelog entries
// up to and including version. Persists to global settings.
// Mirrors upstream setLastChangelogVersion (settings-manager.ts:533-535).
func (sm *SettingsManager) SetLastChangelogVersion(version string) error {
	return sm.UpdateGlobal(func(s *Settings) {
		s.LastChangelogVersion = version
	})
}

// GetSteeringMode returns the steering queue dispatch mode.
// Default: "one-at-a-time". Mirrors upstream getSteeringMode (settings-manager.ts:581).
func (sm *SettingsManager) GetSteeringMode() string {
	m := sm.Get().SteeringMode
	if m == "" {
		return "one-at-a-time"
	}
	return m
}

// GetFollowUpMode returns the follow-up queue dispatch mode.
// Default: "one-at-a-time". Mirrors upstream getFollowUpMode (settings-manager.ts:591).
func (sm *SettingsManager) GetFollowUpMode() string {
	m := sm.Get().FollowUpMode
	if m == "" {
		return "one-at-a-time"
	}
	return m
}

// GetTuiMode returns the terminal UI mode: "regular" only when the setting says so, otherwise "fullscreen" (settings-manager.ts:1348-1350).
func (sm *SettingsManager) GetTuiMode() string {
	if sm.Get().TuiMode == "regular" {
		return "regular"
	}
	return "fullscreen"
}

func (sm *SettingsManager) GetFullscreenExitOutput() string {
	if sm.Get().FullscreenExitOutput == "resume-hint" {
		return "resume-hint"
	}
	return "transcript"
}

func (sm *SettingsManager) GetFullscreenCopyOnSelect() bool {
	value := sm.Get().FullscreenCopyOnSelect
	return value == nil || *value
}

func (sm *SettingsManager) GetFullscreenScrollbar() string {
	mode := sm.Get().FullscreenScrollbar
	if mode == "always" || mode == "hidden" {
		return mode
	}
	return "auto"
}

// GetFullscreenWheelScrollLines returns the wheel line setting: a finite number floored and clamped to 1-100, otherwise auto.
// Mirrors upstream getFullscreenWheelScrollLines (settings-manager.ts:1386-1391).
func (sm *SettingsManager) GetFullscreenWheelScrollLines() WheelScrollLines {
	raw := sm.Get().FullscreenWheelScrollLines
	var lines float64
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &lines) != nil || math.IsNaN(lines) || math.IsInf(lines, 0) {
		return WheelScrollLines{Auto: true}
	}
	return WheelScrollLines{Lines: clampWheelScrollLines(lines)}
}

// clampWheelScrollLines is Math.max(1, Math.min(100, Math.floor(lines))).
func clampWheelScrollLines(lines float64) float64 {
	return math.Max(1, math.Min(100, math.Floor(lines)))
}

// SetFullscreenWheelScrollLines saves the wheel line setting to global settings: auto, or a count floored and clamped to 1-100.
// Mirrors upstream setFullscreenWheelScrollLines (settings-manager.ts:1394-1399).
func (sm *SettingsManager) SetFullscreenWheelScrollLines(lines WheelScrollLines) error {
	value := []byte(`"auto"`)
	if !lines.Auto {
		clamped := clampWheelScrollLines(lines.Lines)
		if math.IsNaN(clamped) {
			// JSON.stringify writes NaN as null, which reads back as auto.
			value = []byte("null")
		} else {
			value = strconv.AppendFloat(nil, clamped, 'f', -1, 64)
		}
	}
	return sm.UpdateGlobal(func(s *Settings) { s.FullscreenWheelScrollLines = value })
}

// GetOrCreateDeviceID returns the stable ID of this installation, e.g. sent to OpenAI as its agent host ID, and creates it on first use.
// Project settings are ignored so a committed project settings file cannot give every clone the same ID.
// A global file that does not parse keeps the new ID in memory only, as upstream's save() returns before writing then.
// Mirrors upstream getOrCreateDeviceId (settings-manager.ts:1172-1179).
func (sm *SettingsManager) GetOrCreateDeviceID() string {
	sm.mu.RLock()
	id := sm.global.DeviceID
	sm.mu.RUnlock()
	if id != "" {
		return id
	}
	id = uuid.NewString()
	setID := func(s *Settings) {
		if s.DeviceID == "" {
			s.DeviceID = id
		}
	}
	if err := sm.UpdateGlobal(setID); err != nil {
		sm.mu.Lock()
		setID(&sm.global)
		sm.merged = mergeSettings(sm.global, sm.project)
		sm.mu.Unlock()
	}
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.global.DeviceID
}

// GetSettings returns a copy of the effective settings: global and project settings merged, with overrides.
// Mirrors upstream getSettings (settings-manager.ts:564-566).
func (sm *SettingsManager) GetSettings() Settings { return sm.Get() }

// ExtensionSettings is GetSettings as the JSON object extensions read: a copy that a change does not reach back into the manager through.
//
// upstream: settings-manager.ts:562-564 (getSettings), loader.ts:411-414
func (sm *SettingsManager) ExtensionSettings() extension.Settings {
	encoded, err := json.Marshal(sm.Get())
	if err != nil {
		return extension.Settings{}
	}
	var settings extension.Settings
	if err := json.Unmarshal(encoded, &settings); err != nil || settings == nil {
		return extension.Settings{}
	}
	return settings
}

func (sm *SettingsManager) GetMermaidRenderingMode() string {
	markdown := sm.Get().Markdown
	if markdown != nil && (markdown.Mermaid == "off" || markdown.Mermaid == "final") {
		return markdown.Mermaid
	}
	return "streaming"
}

// GetEnableSkillCommands returns whether skill commands are shown in autocomplete.
// Default: true. Mirrors upstream getEnableSkillCommands (settings-manager.ts:1264-1266).
func (sm *SettingsManager) GetEnableSkillCommands() bool {
	v := sm.Get().EnableSkillCommands
	if v == nil {
		return true // default: enabled
	}
	return *v
}

// GetCollapseChangelog returns whether to show a condensed changelog.
// Default: false. Mirrors upstream getCollapseChangelog (settings-manager.ts:743).
func (sm *SettingsManager) GetCollapseChangelog() bool {
	return sm.Get().CollapseChangelog
}

// GetTreeFilterMode returns the default /tree filter mode.
// Default: "default". Mirrors upstream SettingsManager.getTreeFilterMode
// (settings-manager.ts:954-955).
func (sm *SettingsManager) GetTreeFilterMode() string {
	m := sm.Get().TreeFilterMode
	switch m {
	case "default", "no-tools", "user-only", "labeled-only", "all":
		return m
	default:
		return "default"
	}
}

// RetryConfig holds resolved retry settings with defaults applied.
type RetryConfig struct {
	Enabled     bool
	MaxRetries  int
	BaseDelayMs int
	MaxDelayMs  int
}

// ProviderRetryConfig holds resolved provider/SDK retry settings.
type ProviderRetryConfig struct {
	TimeoutMs       int
	MaxRetries      int
	MaxRetryDelayMs int
}

// GetRetrySettings returns retry settings with defaults applied.
// Mirrors upstream SettingsManager.getRetrySettings (settings-manager.ts:683-690).
func (sm *SettingsManager) GetRetrySettings() RetryConfig {
	result := RetryConfig{
		Enabled:     true,
		MaxRetries:  3,     // upstream default: 3 (settings-manager.ts:21)
		BaseDelayMs: 2000,  // upstream default: 2000ms (settings-manager.ts:22)
		MaxDelayMs:  60000, // upstream default cap for exponential retry backoff
	}
	s := sm.Get()
	if s.Retry != nil {
		if s.Retry.Enabled != nil {
			result.Enabled = *s.Retry.Enabled
		}
		if s.Retry.MaxRetries != nil {
			result.MaxRetries = *s.Retry.MaxRetries
		}
		if s.Retry.BaseDelayMs != nil {
			result.BaseDelayMs = *s.Retry.BaseDelayMs
		}
		if s.Retry.MaxAgentDelayMs != nil {
			result.MaxDelayMs = *s.Retry.MaxAgentDelayMs
		}
	}
	return result
}

// GetProviderRetrySettings returns provider retry settings with defaults
// applied. timeoutMs maps onto pig's net/http idle deadline
// (GetProviderRequestTimeoutMs); maxRetries/maxRetryDelayMs drive the provider
// retry transport (ai.ConfigureProviderRetry), mirroring the values upstream
// sdk.ts passes into retryProviderRequest.
func (sm *SettingsManager) GetProviderRetrySettings() ProviderRetryConfig {
	result := ProviderRetryConfig{MaxRetryDelayMs: 60000}
	s := sm.Get()
	if s.Retry != nil && s.Retry.Provider != nil {
		if s.Retry.Provider.TimeoutMs != nil {
			result.TimeoutMs = *s.Retry.Provider.TimeoutMs
		}
		if s.Retry.Provider.MaxRetries != nil {
			result.MaxRetries = *s.Retry.Provider.MaxRetries
		}
		// nil means unset (keep the 60s default); an explicit value passes
		// through, including 0 which disables the cap, mirroring upstream's
		// maxRetryDelayMs ?? 60000.
		if s.Retry.Provider.MaxRetryDelayMs != nil {
			result.MaxRetryDelayMs = *s.Retry.Provider.MaxRetryDelayMs
		}
	}
	return result
}

// GetRetryEnabled returns whether automatic retries are enabled.
func (sm *SettingsManager) GetRetryEnabled() bool { return sm.GetRetrySettings().Enabled }

// GetWebSocketConnectTimeoutMs returns the optional opening-handshake timeout; zero disables it.
func (sm *SettingsManager) GetWebSocketConnectTimeoutMs() (*int, error) {
	s := sm.Get()
	if s.webSocketConnectTimeoutNull {
		return nil, fmt.Errorf("Invalid websocketConnectTimeoutMs setting: null")
	}
	if s.webSocketConnectTimeoutInvalid != nil {
		return nil, fmt.Errorf("Invalid websocketConnectTimeoutMs setting: %v", s.webSocketConnectTimeoutInvalid)
	}
	if s.WebSocketConnectTimeoutMs == nil {
		return nil, nil
	}
	if *s.WebSocketConnectTimeoutMs < 0 {
		return nil, fmt.Errorf("Invalid websocketConnectTimeoutMs setting: %v", *s.WebSocketConnectTimeoutMs)
	}
	return cloneIntPtr(s.WebSocketConnectTimeoutMs), nil
}

func webSocketConnectTimeoutWire(s Settings) any {
	if s.webSocketConnectTimeoutNull {
		return json.RawMessage("null")
	}
	if s.WebSocketConnectTimeoutMs != nil {
		return *s.WebSocketConnectTimeoutMs
	}
	return s.webSocketConnectTimeoutInvalid
}

// GetHttpIdleTimeoutMs returns the HTTP header/body idle timeout in milliseconds.
// Default: 300000 (5 minutes). Zero disables the timeout.
// An unparseable value is an error, as upstream parseTimeoutSetting throws.
func (sm *SettingsManager) GetHttpIdleTimeoutMs() (int, error) {
	s := sm.Get()
	if s.httpIdleTimeoutInvalid != nil {
		return 0, fmt.Errorf("Invalid httpIdleTimeoutMs setting: %v", s.httpIdleTimeoutInvalid)
	}
	if s.HTTPIdleTimeoutMs == nil {
		return defaultHTTPIdleTimeoutMs, nil
	}
	timeoutMs, ok := parseHTTPIdleTimeoutMs(*s.HTTPIdleTimeoutMs)
	if !ok {
		return 0, fmt.Errorf("Invalid httpIdleTimeoutMs setting: %v", *s.HTTPIdleTimeoutMs)
	}
	return timeoutMs, nil
}

// httpIdleTimeoutWire returns the httpIdleTimeoutMs value to serialize: the
// parsed value, else the unparseable raw value unchanged.
func httpIdleTimeoutWire(s Settings) any {
	if s.HTTPIdleTimeoutMs != nil {
		return *s.HTTPIdleTimeoutMs
	}
	return s.httpIdleTimeoutInvalid
}

// GetProviderRequestTimeoutMs returns the effective per-request stream/read
// timeout: retry.provider.timeoutMs when set, otherwise the httpIdleTimeoutMs.
// Mirrors upstream sdk.ts:311 (timeoutMs = providerRetrySettings.timeoutMs ??
// effectiveTimeoutMs, where effectiveTimeoutMs is the httpIdleTimeoutMs). The
// sibling maxRetries/maxRetryDelayMs are consumed separately by the provider
// retry transport (ai.ConfigureProviderRetry).
func (sm *SettingsManager) GetProviderRequestTimeoutMs() (int, error) {
	if s := sm.Get(); s.Retry != nil && s.Retry.Provider != nil && s.Retry.Provider.TimeoutMs != nil {
		return *s.Retry.Provider.TimeoutMs, nil
	}
	return sm.GetHttpIdleTimeoutMs()
}

// GetSessionDir returns the configured session directory, expanding ~ forms
// and converting a file:// URL. An invalid file URL is an error, as upstream
// getSessionDir throws Node's fileURLToPath error.
func (sm *SettingsManager) GetSessionDir() (string, error) {
	return normalizeSettingsPath(sm.Get().SessionDir)
}

// normalizeSettingsPath mirrors upstream utils/paths.ts normalizePath with
// default options: a leading "~" or "~/" expands to the home directory and a
// file:// URL becomes its path through Node's fileURLToPath, whose errors it
// returns. An empty path stays empty.
func normalizeSettingsPath(path string) (string, error) {
	return resolvepath.Normalize(path)
}

// GetDefaultProvider returns the configured default provider, or "".
func (sm *SettingsManager) GetDefaultProvider() string { return sm.Get().DefaultProvider }

// GetDefaultModel returns the configured default model, or "".
func (sm *SettingsManager) GetDefaultModel() string { return sm.Get().DefaultModel }

// GetThemeSetting returns the selected setting, preserving an omitted value separately from an explicitly empty name.
func (sm *SettingsManager) GetThemeSetting() *string { return sm.Get().themeSetting() }

// GetTheme returns the configured fixed theme name, or "" for an automatic slash-separated theme setting.
func (sm *SettingsManager) GetTheme() string {
	theme := sm.GetThemeSetting()
	if theme == nil || strings.Contains(*theme, "/") {
		return ""
	}
	return *theme
}

// GetDefaultThinkingLevel returns the configured thinking level, or "".
func (sm *SettingsManager) GetDefaultThinkingLevel() string { return sm.Get().DefaultThinkingLevel }

// GetTransport returns the configured transport. Default: "auto".
func (sm *SettingsManager) GetTransport() string {
	t := sm.Get().Transport
	if t == "" {
		return "auto"
	}
	return t
}

// GetHideThinkingBlock returns whether thinking blocks should be hidden.
func (sm *SettingsManager) GetHideThinkingBlock() bool { return sm.Get().HideThinkingBlock }

// GetShowCacheMissNotices returns whether transcript cache-miss notices are shown.
func (sm *SettingsManager) GetShowCacheMissNotices() bool { return sm.Get().ShowCacheMissNotices }

// GetShellPath returns the configured shell path (for bash tool). An invalid
// file URL is an error, as upstream getShellPath throws.
func (sm *SettingsManager) GetShellPath() (string, error) {
	return normalizeSettingsPath(sm.Get().ShellPath)
}

// GetQuietStartup returns the quiet startup setting: true, "header" or false (settings-manager.ts:1089-1092).
func (sm *SettingsManager) GetQuietStartup() QuietStartup { return sm.Get().QuietStartup }

// GetShellCommandPrefix returns the command prefix (e.g. "set -e; ").
func (sm *SettingsManager) GetShellCommandPrefix() string { return sm.Get().CommandPrefix }

// GetNpmCommand returns the npm command override. Default: nil (use "npm").
func (sm *SettingsManager) GetNpmCommand() []string { return sm.Get().NpmCommand }

// GetEnableAnalytics reports the opt-in analytics setting (default false).
// Mirrors upstream SettingsManager.getEnableAnalytics.
func (sm *SettingsManager) GetEnableAnalytics() bool {
	enabled := sm.Get().EnableAnalytics
	return enabled != nil && *enabled
}

// GetTrackingID returns the analytics tracking identifier, or "". Mirrors
// upstream getTrackingId.
func (sm *SettingsManager) GetTrackingID() string { return sm.Get().TrackingID }

// SetEnableAnalytics saves the analytics opt-in. The first opt-in generates a
// random UUID tracking identifier, which later toggles keep. Mirrors upstream
// setEnableAnalytics.
func (sm *SettingsManager) SetEnableAnalytics(enabled bool) error {
	return sm.UpdateGlobal(func(s *Settings) {
		s.EnableAnalytics = &enabled
		if enabled && s.TrackingID == "" {
			s.TrackingID = uuid.NewString()
		}
	})
}

// GetEnableInstallTelemetry returns whether package-install telemetry is enabled.
// Default: true. This only answers the setting/env gate; it does not imply a
// transport is configured or that telemetry will actually be delivered.
func (sm *SettingsManager) GetEnableInstallTelemetry() bool {
	if sm.Get().EnableInstallTelemetry == nil {
		return true
	}
	return *sm.Get().EnableInstallTelemetry
}

// IsInstallTelemetryEnabled reports whether install/attribution telemetry is
// enabled, honoring the PI_TELEMETRY env override first and otherwise the
// enableInstallTelemetry setting (default true). Mirrors upstream
// isInstallTelemetryEnabled (telemetry.ts:8): when PI_TELEMETRY is set its
// truthiness wins; otherwise the setting decides. Gates provider attribution
// headers (provider-attribution.ts) in addition to package-install telemetry.
// pig divergence (D26): gates the OpenRouter/NVIDIA/Cloudflare attribution headers in coding/model.go.
func (sm *SettingsManager) IsInstallTelemetryEnabled() bool {
	if env, ok := os.LookupEnv("PI_TELEMETRY"); ok {
		return isTruthyTelemetryEnvFlag(env)
	}
	return sm.GetEnableInstallTelemetry()
}

// isTruthyTelemetryEnvFlag mirrors upstream isTruthyEnvFlag (telemetry.ts:3):
// "1", "true", or "yes" (case-insensitive) are truthy; everything else, including
// empty, is falsy.
// pig divergence (D26): telemetry gate feeding the attribution-header decision.
func isTruthyTelemetryEnvFlag(value string) bool {
	if value == "" {
		return false
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// GetPackages returns the configured package sources, or an empty list.
func (sm *SettingsManager) GetPackages() []PackageSource {
	packages := sm.Get().Packages
	if packages == nil {
		return []PackageSource{}
	}
	return packages
}

// GetExtensionPaths returns the configured extension paths, or an empty list.
func (sm *SettingsManager) GetExtensionPaths() []string {
	paths := sm.Get().Extensions
	if paths == nil {
		return []string{}
	}
	return paths
}

// GetSkillPaths returns the configured skill paths, or an empty list.
func (sm *SettingsManager) GetSkillPaths() []string {
	paths := sm.Get().Skills
	if paths == nil {
		return []string{}
	}
	return paths
}

// GetPromptTemplatePaths returns the configured prompt template paths, or an empty list.
func (sm *SettingsManager) GetPromptTemplatePaths() []string {
	paths := sm.Get().Prompts
	if paths == nil {
		return []string{}
	}
	return paths
}

// GetThemePaths returns the configured theme paths, or an empty list.
func (sm *SettingsManager) GetThemePaths() []string {
	paths := sm.Get().Themes
	if paths == nil {
		return []string{}
	}
	return paths
}

// GetThinkingBudgets returns custom thinking budgets, or nil.
func (sm *SettingsManager) GetThinkingBudgets() *ThinkingBudgetsSettings {
	return sm.Get().ThinkingBudgets
}

// GetShowImages returns whether inline images should be rendered.
func (sm *SettingsManager) GetShowImages() bool { return sm.Get().GetShowImages() }

// GetImageWidthCells returns the max image width in columns.
func (sm *SettingsManager) GetImageWidthCells() int { return sm.Get().GetImageWidthCells() }

// GetClearOnShrink returns whether screen clears on terminal shrink.
func (sm *SettingsManager) GetClearOnShrink() bool { return sm.Get().GetClearOnShrink() }

// GetShowTerminalProgress returns whether OSC 9;4 terminal progress indicators are enabled.
func (sm *SettingsManager) GetShowTerminalProgress() bool { return sm.Get().GetShowTerminalProgress() }

// GetTerminalCapabilityOverrides mirrors upstream
// SettingsManager.getTerminalCapabilityOverrides over the merged settings.
func (sm *SettingsManager) GetTerminalCapabilityOverrides() tui.CapabilityOverrides {
	return sm.Get().GetTerminalCapabilityOverrides()
}

// GetImageAutoResize returns whether images auto-resize.
func (sm *SettingsManager) GetImageAutoResize() bool { return sm.Get().GetImageAutoResize() }

// GetBlockImages returns whether image rendering is blocked entirely.
func (sm *SettingsManager) GetBlockImages() bool { return sm.Get().BlockImages }

// GetDefaultTools returns the configured initial built-in tool selection, or
// nil. Mirrors upstream SettingsManager.getDefaultTools.
func (sm *SettingsManager) GetDefaultTools() []string { return sm.Get().GetDefaultTools() }

// ResolvedDefaultTools returns the tools enabled at startup: the `defaultTools` setting, or the built-in defaults when no settings layer sets it.
// Mirrors upstream `getDefaultTools() ?? DEFAULT_TOOL_NAMES` (agent-session.ts:3593).
func (sm *SettingsManager) ResolvedDefaultTools() []string {
	if tools := sm.GetDefaultTools(); tools != nil {
		return tools
	}
	return slices.Clone(defaultToolNames)
}

// GetDefaultTools returns the resolved `defaultTools` selection, or nil when no settings layer sets it: plain names replace the built-in defaults, then `+name` and `-name` entries apply in order.
// Mirrors upstream getDefaultTools (settings-manager.ts:1430-1434).
func (s Settings) GetDefaultTools() []string {
	if s.DefaultTools == nil {
		return nil
	}
	return resolveDefaultTools(s.DefaultTools)
}

// GetModelThinkingLevel returns the per-model default thinking level for
// provider/modelID, or "". Mirrors upstream getModelThinkingLevel.
func (sm *SettingsManager) GetModelThinkingLevel(provider, modelID string) string {
	return sm.Get().ModelThinkingLevels[provider+"/"+modelID]
}

// SetModelThinkingLevel sets a per-model default thinking level override,
// keyed by "provider/modelID". Mirrors upstream setModelThinkingLevel.
func (sm *SettingsManager) SetModelThinkingLevel(provider, modelID, level string) error {
	return sm.UpdateGlobal(func(s *Settings) {
		if s.ModelThinkingLevels == nil {
			s.ModelThinkingLevels = map[string]string{}
		}
		s.ModelThinkingLevels[provider+"/"+modelID] = level
	})
}

// RemoveModelThinkingLevel clears a per-model default thinking level
// override. Mirrors upstream removeModelThinkingLevel.
func (sm *SettingsManager) RemoveModelThinkingLevel(provider, modelID string) error {
	return sm.UpdateGlobal(func(s *Settings) {
		if s.ModelThinkingLevels == nil {
			return
		}
		delete(s.ModelThinkingLevels, provider+"/"+modelID)
		if len(s.ModelThinkingLevels) == 0 {
			s.ModelThinkingLevels = nil
		}
	})
}

// GetEnabledModels returns the list of enabled models for Ctrl+P cycling, or nil.
func (sm *SettingsManager) GetEnabledModels() []string { return sm.Get().EnabledModels }

// GetShowHardwareCursor returns whether the hardware cursor is shown.
func (sm *SettingsManager) GetShowHardwareCursor() bool { return sm.Get().GetShowHardwareCursor() }

// GetEditorPaddingX returns horizontal editor padding.
func (sm *SettingsManager) GetEditorPaddingX() int { return sm.Get().GetEditorPaddingX() }

// GetOutputPad returns horizontal chat output padding.
func (sm *SettingsManager) GetOutputPad() int { return sm.Get().GetOutputPad() }

// GetExternalEditorCommand resolves a nonblank configured command, VISUAL, EDITOR, then the platform default, without trimming the selected command.
func (sm *SettingsManager) GetExternalEditorCommand() string {
	return resolveExternalEditorCommand(sm.Get().ExternalEditor, os.Getenv("VISUAL"), os.Getenv("EDITOR"), runtime.GOOS)
}

// upstream: packages/coding-agent/src/core/settings-manager.ts:getExternalEditorCommand
func resolveExternalEditorCommand(configured, visual, editor, goos string) string {
	if jsTrim(configured) != "" {
		return configured
	}
	if visual != "" {
		return visual
	}
	if editor != "" {
		return editor
	}
	if goos == "windows" {
		return "notepad"
	}
	return "nano"
}

// GetAutocompleteMaxVisible returns max autocomplete items visible.
func (sm *SettingsManager) GetAutocompleteMaxVisible() int {
	return sm.Get().GetAutocompleteMaxVisible()
}

// GetCodeBlockIndent returns the markdown code block indent string.
func (sm *SettingsManager) GetCodeBlockIndent() string { return sm.Get().GetCodeBlockIndent() }

// GetWarnings returns warning settings.
func (sm *SettingsManager) GetWarnings() WarningSettings {
	warnings := sm.Get().Warnings
	if warnings == nil || !warnings.anthropicExtraUsageSet {
		return WarningSettings{AnthropicExtraUsage: true, anthropicExtraUsageSet: true}
	}
	return *cloneWarningSettings(warnings)
}

// ─── Setters (all persist to global settings) ─────────────────────────────────

// SetDefaultProvider sets the default provider.
func (sm *SettingsManager) SetDefaultProvider(p string) error {
	return sm.UpdateGlobal(func(s *Settings) { s.DefaultProvider = p })
}

// SetDefaultModel sets the default model.
func (sm *SettingsManager) SetDefaultModel(m string) error {
	return sm.UpdateGlobal(func(s *Settings) { s.DefaultModel = m })
}

// SetDefaultModelAndProvider sets both default model and provider atomically.
func (sm *SettingsManager) SetDefaultModelAndProvider(provider, model string) error {
	return sm.UpdateGlobal(func(s *Settings) {
		s.DefaultProvider = provider
		s.DefaultModel = model
	})
}

// SetSteeringMode sets the steering queue dispatch mode.
func (sm *SettingsManager) SetSteeringMode(mode string) error {
	return sm.UpdateGlobal(func(s *Settings) { s.SteeringMode = mode })
}

// SetFollowUpMode sets the follow-up queue dispatch mode.
func (sm *SettingsManager) SetFollowUpMode(mode string) error {
	return sm.UpdateGlobal(func(s *Settings) { s.FollowUpMode = mode })
}

// SetTuiMode sets the terminal UI mode.
func (sm *SettingsManager) SetTuiMode(mode string) error {
	return sm.UpdateGlobal(func(s *Settings) { s.TuiMode = mode })
}

func (sm *SettingsManager) SetFullscreenExitOutput(output string) error {
	return sm.UpdateGlobal(func(s *Settings) { s.FullscreenExitOutput = output })
}

func (sm *SettingsManager) SetFullscreenCopyOnSelect(enabled bool) error {
	return sm.UpdateGlobal(func(s *Settings) { s.FullscreenCopyOnSelect = &enabled })
}

func (sm *SettingsManager) SetFullscreenScrollbar(mode string) error {
	return sm.UpdateGlobal(func(s *Settings) { s.FullscreenScrollbar = mode })
}

func (sm *SettingsManager) SetMermaidRenderingMode(mode string) error {
	return sm.UpdateGlobal(func(s *Settings) {
		if s.Markdown == nil {
			s.Markdown = &MarkdownSettings{}
		}
		s.Markdown.Mermaid = mode
	})
}

// SetTheme stores an explicit default theme; an empty name is not an omitted setting.
func (sm *SettingsManager) SetTheme(theme string) error {
	return sm.UpdateGlobal(func(s *Settings) {
		s.Theme = theme
		s.themeEmpty = theme == ""
		s.themeNonString = false
	})
}

// SetDefaultThinkingLevel sets the default thinking level.
func (sm *SettingsManager) SetDefaultThinkingLevel(level string) error {
	return sm.UpdateGlobal(func(s *Settings) { s.DefaultThinkingLevel = level })
}

// SetTransport sets the HTTP transport mode.
func (sm *SettingsManager) SetTransport(transport string) error {
	return sm.UpdateGlobal(func(s *Settings) { s.Transport = transport })
}

// SetCompactionEnabled enables or disables auto-compaction.
func (sm *SettingsManager) SetCompactionEnabled(enabled bool) error {
	return sm.UpdateGlobal(func(s *Settings) {
		if s.Compaction == nil {
			s.Compaction = &CompactionSettingsJSON{}
		}
		s.Compaction.Enabled = &enabled
	})
}

// SetRetryEnabled enables or disables auto-retry.
func (sm *SettingsManager) SetRetryEnabled(enabled bool) error {
	return sm.UpdateGlobal(func(s *Settings) {
		if s.Retry == nil {
			s.Retry = &RetrySettingsJSON{}
		}
		s.Retry.Enabled = &enabled
	})
}

// SetHttpIdleTimeoutMs sets the HTTP header/body idle timeout in milliseconds.
// Zero disables the timeout.
func (sm *SettingsManager) SetHttpIdleTimeoutMs(timeoutMs float64) error {
	normalized, ok := parseHTTPIdleTimeoutMs(timeoutMs)
	if !ok {
		return fmt.Errorf("invalid httpIdleTimeoutMs setting: %v", timeoutMs)
	}
	return sm.UpdateGlobal(func(s *Settings) {
		s.HTTPIdleTimeoutMs = &normalized
		s.httpIdleTimeoutInvalid = nil
	})
}

// SetHideThinkingBlock sets whether to hide thinking blocks.
func (sm *SettingsManager) SetHideThinkingBlock(hide bool) error {
	return sm.UpdateGlobal(func(s *Settings) {
		s.HideThinkingBlock = hide
		s.hideThinkingBlockSet = true
	})
}

// SetShowCacheMissNotices sets whether transcript cache-miss notices are shown.
func (sm *SettingsManager) SetShowCacheMissNotices(show bool) error {
	return sm.UpdateGlobal(func(s *Settings) {
		s.ShowCacheMissNotices = show
		s.showCacheMissNoticesSet = true
	})
}

// SetShellPath sets the shell path for the bash tool.
func (sm *SettingsManager) SetShellPath(path string) error {
	return sm.UpdateGlobal(func(s *Settings) { s.ShellPath = path })
}

// SetQuietStartup sets the global quiet startup setting.
func (sm *SettingsManager) SetQuietStartup(quiet QuietStartup) error {
	return sm.UpdateGlobal(func(s *Settings) {
		s.QuietStartup = quiet
		s.quietStartupSet = true
	})
}

// SetShellCommandPrefix sets the command prefix for bash commands.
func (sm *SettingsManager) SetShellCommandPrefix(prefix string) error {
	return sm.UpdateGlobal(func(s *Settings) { s.CommandPrefix = prefix })
}

// SetNpmCommand sets the npm command override.
func (sm *SettingsManager) SetNpmCommand(cmd []string) error {
	return sm.UpdateGlobal(func(s *Settings) { s.NpmCommand = cmd })
}

// SetCollapseChangelog sets whether changelog is collapsed.
func (sm *SettingsManager) SetCollapseChangelog(collapse bool) error {
	return sm.UpdateGlobal(func(s *Settings) {
		s.CollapseChangelog = collapse
		s.collapseChangelogSet = true
	})
}

// SetEnableInstallTelemetry sets whether package-install telemetry is enabled.
// This toggles the gate only; delivery depends on the configured sender.
func (sm *SettingsManager) SetEnableInstallTelemetry(enabled bool) error {
	return sm.UpdateGlobal(func(s *Settings) { s.EnableInstallTelemetry = &enabled })
}

// SetEnableSkillCommands sets whether skill commands are enabled.
func (sm *SettingsManager) SetEnableSkillCommands(enabled bool) error {
	return sm.UpdateGlobal(func(s *Settings) { s.EnableSkillCommands = &enabled })
}

// SetShowImages sets whether inline images are shown.
func (sm *SettingsManager) SetShowImages(show bool) error {
	return sm.UpdateGlobal(func(s *Settings) { s.ShowImages = &show })
}

// SetImageWidthCells sets the max image width in columns.
func (sm *SettingsManager) SetImageWidthCells(width int) error {
	return sm.UpdateGlobal(func(s *Settings) { s.ImageWidthCells = width })
}

// SetClearOnShrink sets whether screen clears on terminal shrink.
func (sm *SettingsManager) SetClearOnShrink(enabled bool) error {
	return sm.UpdateGlobal(func(s *Settings) { s.ClearOnShrink = &enabled })
}

// SetShowTerminalProgress sets whether OSC 9;4 terminal progress indicators are enabled.
func (sm *SettingsManager) SetShowTerminalProgress(enabled bool) error {
	return sm.UpdateGlobal(func(s *Settings) { s.ShowTerminalProgress = &enabled })
}

// SetImageAutoResize sets whether images auto-resize.
func (sm *SettingsManager) SetImageAutoResize(enabled bool) error {
	return sm.UpdateGlobal(func(s *Settings) { s.ImageAutoResize = &enabled })
}

// SetBlockImages sets whether image rendering is blocked.
func (sm *SettingsManager) SetBlockImages(blocked bool) error {
	return sm.UpdateGlobal(func(s *Settings) {
		s.BlockImages = blocked
		s.blockImagesSet = true
	})
}

// SetEnabledModels sets the list of enabled models for Ctrl+P cycling.
func (sm *SettingsManager) SetEnabledModels(patterns []string) error {
	return sm.UpdateGlobal(func(s *Settings) { s.EnabledModels = patterns })
}

// SetDoubleEscapeAction sets the double-escape action.
func (sm *SettingsManager) SetDoubleEscapeAction(action string) error {
	return sm.UpdateGlobal(func(s *Settings) { s.DoubleEscapeAction = action })
}

// SetTreeFilterMode sets the tree filter mode.
func (sm *SettingsManager) SetTreeFilterMode(mode string) error {
	return sm.UpdateGlobal(func(s *Settings) { s.TreeFilterMode = mode })
}

// SetShowHardwareCursor sets whether the hardware cursor is shown.
func (sm *SettingsManager) SetShowHardwareCursor(enabled bool) error {
	return sm.UpdateGlobal(func(s *Settings) { s.ShowHardwareCursor = &enabled })
}

// SetEditorPaddingX sets horizontal editor padding. Clamped to 0-3.
func (sm *SettingsManager) SetEditorPaddingX(padding int) error {
	p := max(0, min(3, padding))
	return sm.UpdateGlobal(func(s *Settings) { s.EditorPaddingX = &p })
}

// SetOutputPad sets horizontal chat output padding. Clamped to 0-1.
func (sm *SettingsManager) SetOutputPad(padding int) error {
	p := max(0, min(1, padding))
	return sm.UpdateGlobal(func(s *Settings) { s.OutputPad = &p })
}

// SetAutocompleteMaxVisible sets max autocomplete items. Clamped to 3-20.
func (sm *SettingsManager) SetAutocompleteMaxVisible(n int) error {
	v := max(3, min(20, n))
	return sm.UpdateGlobal(func(s *Settings) { s.AutocompleteMaxVisible = &v })
}

// SetWarnings sets warning settings.
func (sm *SettingsManager) SetWarnings(warnings WarningSettings) error {
	warnings.anthropicExtraUsageSet = true
	return sm.UpdateGlobal(func(s *Settings) { s.Warnings = &warnings })
}

// GlobalPath returns the global settings file path, or empty for memory storage.
func (sm *SettingsManager) GlobalPath() string {
	if sm.memory != nil {
		return ""
	}
	return filepath.Join(sm.agentDir, "settings.json")
}

// UpdateGlobal applies fn and refreshes the merged view, then saves to the selected backing storage. Like Pi's setters (settings-manager.ts:668-682), the change stays in effect in memory when the global file had parse errors, which skips the save, or when the save fails.
func (sm *SettingsManager) UpdateGlobal(fn func(*Settings)) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	before := cloneSettings(sm.global)
	fn(&sm.global)
	sm.merged = mergeSettings(sm.global, sm.project)
	if sm.memory != nil {
		sm.memory.global = cloneSettings(sm.global)
		return nil
	}
	if sm.globalLoadErr != nil {
		return fmt.Errorf("global settings file has parse errors: %w", sm.globalLoadErr)
	}
	path := filepath.Join(sm.agentDir, "settings.json")
	if err := saveSettingsPatch(path, before, sm.global); err != nil {
		sm.errors = append(sm.errors, SettingsError{Scope: "global", Path: path, Error: err})
		return err
	}
	return nil
}

// UpdateProject applies fn to trusted project settings and refreshes the merged view, then saves to the selected storage. Like Pi's saveProjectSettings (settings-manager.ts:684-698), an untrusted project refuses the change, and otherwise the change stays in effect in memory when the project file had parse errors, which skips the save, or when the save fails.
func (sm *SettingsManager) UpdateProject(fn func(*Settings)) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if !sm.projectTrusted {
		return errors.New("Project is not trusted; refusing to write project settings")
	}
	before := cloneSettings(sm.project)
	fn(&sm.project)
	sm.merged = mergeSettings(sm.global, sm.project)
	if sm.memory != nil {
		sm.memory.project = cloneSettings(sm.project)
		return nil
	}
	if sm.projectLoadErr != nil {
		return fmt.Errorf("project settings file has parse errors: %w", sm.projectLoadErr)
	}
	path := filepath.Join(ProjectConfigDir(sm.cwd), "settings.json")
	if err := saveSettingsPatch(path, before, sm.project); err != nil {
		sm.errors = append(sm.errors, SettingsError{Scope: "project", Path: path, Error: err})
		return err
	}
	return nil
}

// SetPackages persists the global package source list, including an explicitly empty array.
func (sm *SettingsManager) SetPackages(pkgs []PackageSource) error {
	return sm.UpdateGlobal(func(s *Settings) {
		s.Packages = pkgs
	})
}

// SetProjectPackages persists the project package source list, including an explicitly empty array.
func (sm *SettingsManager) SetProjectPackages(pkgs []PackageSource) error {
	return sm.UpdateProject(func(s *Settings) {
		s.Packages = pkgs
	})
}

func (sm *SettingsManager) SetExtensionPaths(paths []string) error {
	return sm.UpdateGlobal(func(s *Settings) { s.Extensions = paths })
}

func (sm *SettingsManager) SetProjectExtensionPaths(paths []string) error {
	return sm.UpdateProject(func(s *Settings) { s.Extensions = paths })
}

func (sm *SettingsManager) SetSkillPaths(paths []string) error {
	return sm.UpdateGlobal(func(s *Settings) { s.Skills = paths })
}

func (sm *SettingsManager) SetProjectSkillPaths(paths []string) error {
	return sm.UpdateProject(func(s *Settings) { s.Skills = paths })
}

func (sm *SettingsManager) SetPromptTemplatePaths(paths []string) error {
	return sm.UpdateGlobal(func(s *Settings) { s.Prompts = paths })
}

func (sm *SettingsManager) SetProjectPromptTemplatePaths(paths []string) error {
	return sm.UpdateProject(func(s *Settings) { s.Prompts = paths })
}

func (sm *SettingsManager) SetThemePaths(paths []string) error {
	return sm.UpdateGlobal(func(s *Settings) { s.Themes = paths })
}

func (sm *SettingsManager) SetProjectThemePaths(paths []string) error {
	return sm.UpdateProject(func(s *Settings) { s.Themes = paths })
}

// GlobalSettingsPath returns the global settings file path, or empty for memory storage.
func (sm *SettingsManager) GlobalSettingsPath() string { return sm.GlobalPath() }

func loadSettingsFile(path string) (result Settings, err error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return Settings{}, nil
	} else if err != nil {
		return Settings{}, err
	}
	release, err := acquireSyncLockWithRetry(path)
	if err != nil {
		return Settings{}, err
	}
	defer releaseSyncLock(release, &err)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Settings{}, nil
		}
		return Settings{}, err
	}
	if len(data) == 0 {
		return Settings{}, nil
	}
	return parseSettingsJSON(text.StripBomBytes(data))
}

// parseSettingsJSON applies JSON.parse and migrateSettings to settings file content: a syntax error carries V8's
// JSON.parse message, an array reads as no settings, and any other non-object value fails migrateSettings' first
// `in` test.
func parseSettingsJSON(data []byte) (Settings, error) {
	if err := jsonparse.Validate(data); err != nil {
		return Settings{}, err
	}
	switch trimmed := bytes.TrimLeft(data, " \t\r\n"); {
	case len(trimmed) > 0 && trimmed[0] == '[':
		return Settings{}, nil
	case len(trimmed) > 0 && trimmed[0] != '{':
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			return Settings{}, err
		}
		operand := "null"
		switch v := value.(type) {
		case float64:
			if v == 0 {
				operand = "0"
			} else {
				operand = tools.FormatJSNumber(v)
			}
		case string, bool:
			operand = jsStringValue(v)
		}
		return Settings{}, fmt.Errorf("Cannot use 'in' operator to search for 'queueMode' in %s", operand)
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return Settings{}, err
	}
	return s, nil
}

// saveSettingsPatch is FileSettingsStorage.withLock with persistScopedSettings (settings-manager.ts:268-296,643-665): it creates the directory when existsSync does not find it, and returns Node's fs errors and JSON.parse's message for the current file unchanged.
func saveSettingsPatch(path string, before, after Settings) (err error) {
	if dir := filepath.Dir(path); !pathExists(dir) {
		if err := nodeerrno.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	release, err := acquireSyncLockWithRetry(path)
	if err != nil {
		return err
	}
	defer releaseSyncLock(release, &err)

	current := map[string]json.RawMessage{}
	data, readErr := os.ReadFile(path)
	if readErr == nil {
		data = text.StripBomBytes(data)
		if _, err := parseSettingsJSON(data); err != nil {
			return err
		}
		if err := json.Unmarshal(data, &current); err != nil {
			return err
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return nodeerrno.FromPathError(readErr)
	}
	beforeMap, err := settingsJSONMap(before)
	if err != nil {
		return err
	}
	afterMap, err := settingsJSONMap(after)
	if err != nil {
		return err
	}
	if err := patchJSONObject(current, beforeMap, afterMap); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		return nodeerrno.FromPathError(err)
	}
	return nil
}

func settingsJSONMap(settings Settings) (map[string]json.RawMessage, error) {
	encoded, err := json.Marshal(settings)
	if err != nil {
		return nil, err
	}
	result := map[string]json.RawMessage{}
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func patchJSONObject(current, before, after map[string]json.RawMessage) error {
	keys := make(map[string]struct{}, len(after))
	for key := range before {
		keys[key] = struct{}{}
	}
	for key := range after {
		keys[key] = struct{}{}
	}
	for key := range keys {
		oldValue, hadOld := before[key]
		newValue, hasNew := after[key]
		if hadOld == hasNew && bytes.Equal(oldValue, newValue) {
			continue
		}
		if !hasNew {
			delete(current, key)
			continue
		}
		newObject, newIsObject := rawJSONObject(newValue)
		if !newIsObject {
			current[key] = slices.Clone(newValue)
			continue
		}
		oldObject, _ := rawJSONObject(oldValue)
		currentObject, _ := rawJSONObject(current[key])
		if currentObject == nil {
			currentObject = map[string]json.RawMessage{}
		}
		if err := patchJSONObject(currentObject, oldObject, newObject); err != nil {
			return err
		}
		encoded, err := json.Marshal(currentObject)
		if err != nil {
			return err
		}
		current[key] = encoded
	}
	return nil
}

func rawJSONObject(value json.RawMessage) (map[string]json.RawMessage, bool) {
	value = bytes.TrimSpace(value)
	if len(value) == 0 || value[0] != '{' {
		return map[string]json.RawMessage{}, false
	}
	result := map[string]json.RawMessage{}
	if err := json.Unmarshal(value, &result); err != nil {
		return map[string]json.RawMessage{}, false
	}
	return result, true
}

// mergeSettings merges project settings over global settings.
// Mirrors upstream deepMergeSettings: primitives/arrays fully override, nested
// objects merge field-by-field, and presence-tracked booleans may override to
// either true or false.
func mergeSettings(global, project Settings) Settings {
	m := cloneSettings(global)
	if project.DefaultProvider != "" {
		m.DefaultProvider = project.DefaultProvider
	}
	if project.DefaultModel != "" {
		m.DefaultModel = project.DefaultModel
	}
	if project.DefaultThinkingLevel != "" {
		m.DefaultThinkingLevel = project.DefaultThinkingLevel
	}
	if project.Theme != "" || project.themeEmpty || project.themeNonString {
		m.Theme = project.Theme
		m.themeEmpty = project.themeEmpty
		m.themeNonString = project.themeNonString
	}
	if project.ShellPath != "" {
		m.ShellPath = project.ShellPath
	}
	if project.CommandPrefix != "" {
		m.CommandPrefix = project.CommandPrefix
	}
	if project.Transport != "" {
		m.Transport = project.Transport
	}
	if project.SessionDir != "" {
		m.SessionDir = project.SessionDir
	}
	if project.WebSocketConnectTimeoutMs != nil || project.webSocketConnectTimeoutInvalid != nil || project.webSocketConnectTimeoutNull {
		m.webSocketConnectTimeoutNull = project.webSocketConnectTimeoutNull
		m.WebSocketConnectTimeoutMs = cloneIntPtr(project.WebSocketConnectTimeoutMs)
		m.webSocketConnectTimeoutInvalid = project.webSocketConnectTimeoutInvalid
	}
	if project.HTTPIdleTimeoutMs != nil || project.httpIdleTimeoutInvalid != nil {
		m.httpIdleTimeoutInvalid = project.httpIdleTimeoutInvalid
		m.HTTPIdleTimeoutMs = cloneIntPtr(project.HTTPIdleTimeoutMs)
	}
	if project.Packages != nil {
		m.Packages = clonePackageSources(project.Packages)
	}
	if project.Extensions != nil {
		m.Extensions = append([]string{}, project.Extensions...)
	}
	if project.Skills != nil {
		m.Skills = append([]string{}, project.Skills...)
	}
	if project.Prompts != nil {
		m.Prompts = append([]string{}, project.Prompts...)
	}
	if project.Themes != nil {
		m.Themes = append([]string{}, project.Themes...)
	}
	if project.DefaultTools != nil {
		m.DefaultTools = mergeDefaultTools(m.DefaultTools, project.DefaultTools)
	}
	if project.ModelThinkingLevels != nil {
		// Upstream deep-merges nested objects: project keys override global.
		merged := maps.Clone(m.ModelThinkingLevels)
		if merged == nil {
			merged = map[string]string{}
		}
		maps.Copy(merged, project.ModelThinkingLevels)
		m.ModelThinkingLevels = merged
	}
	if project.EnabledModels != nil {
		m.EnabledModels = append([]string{}, project.EnabledModels...)
	}
	if project.NpmCommand != nil {
		m.NpmCommand = append([]string{}, project.NpmCommand...)
	}
	if project.BranchSummary != nil {
		if m.BranchSummary == nil {
			m.BranchSummary = &BranchSummaryConfig{}
		}
		if project.BranchSummary.reserveTokensSet || project.BranchSummary.ReserveTokens != 0 {
			m.BranchSummary.ReserveTokens = project.BranchSummary.ReserveTokens
			m.BranchSummary.reserveTokensSet = true
		}
		if project.BranchSummary.skipPromptSet {
			m.BranchSummary.SkipPrompt = project.BranchSummary.SkipPrompt
			m.BranchSummary.skipPromptSet = true
		}
	}
	if project.Compaction != nil {
		if m.Compaction == nil {
			m.Compaction = &CompactionSettingsJSON{}
		}
		if project.Compaction.Enabled != nil {
			m.Compaction.Enabled = cloneBoolPtr(project.Compaction.Enabled)
		}
		if m.Compaction.extra == nil {
			m.Compaction.extra = make(map[string]json.RawMessage)
		}
		maps.Copy(m.Compaction.extra, project.Compaction.extra)
		mergeCompactionNumber(&m.Compaction.ReserveTokens, m.Compaction.extra, "reserveTokens", project.Compaction.ReserveTokens, project.Compaction.extra)
		mergeCompactionNumber(&m.Compaction.KeepRecentTokens, m.Compaction.extra, "keepRecentTokens", project.Compaction.KeepRecentTokens, project.Compaction.extra)
		mergeCompactionModelOverrides(m.Compaction, project.Compaction.ModelOverrides)
	}
	if project.Retry != nil {
		if m.Retry == nil {
			m.Retry = &RetrySettingsJSON{}
		}
		if project.Retry.Enabled != nil {
			m.Retry.Enabled = cloneBoolPtr(project.Retry.Enabled)
		}
		if project.Retry.MaxRetries != nil {
			m.Retry.MaxRetries = cloneIntPtr(project.Retry.MaxRetries)
		}
		if project.Retry.BaseDelayMs != nil {
			m.Retry.BaseDelayMs = cloneIntPtr(project.Retry.BaseDelayMs)
		}
		if project.Retry.MaxAgentDelayMs != nil {
			m.Retry.MaxAgentDelayMs = cloneIntPtr(project.Retry.MaxAgentDelayMs)
		}
		if project.Retry.Provider != nil {
			if m.Retry.Provider == nil {
				m.Retry.Provider = &ProviderRetrySettings{}
			}
			if project.Retry.Provider.TimeoutMs != nil {
				m.Retry.Provider.TimeoutMs = cloneIntPtr(project.Retry.Provider.TimeoutMs)
			}
			if project.Retry.Provider.MaxRetries != nil {
				m.Retry.Provider.MaxRetries = cloneIntPtr(project.Retry.Provider.MaxRetries)
			}
			if project.Retry.Provider.MaxRetryDelayMs != nil {
				d := *project.Retry.Provider.MaxRetryDelayMs
				m.Retry.Provider.MaxRetryDelayMs = &d
			}
		}
	}
	if project.DoubleEscapeAction != "" {
		m.DoubleEscapeAction = project.DoubleEscapeAction
	}
	if project.TreeFilterMode != "" {
		m.TreeFilterMode = project.TreeFilterMode
	}
	if project.SteeringMode != "" {
		m.SteeringMode = project.SteeringMode
	}
	if project.FollowUpMode != "" {
		m.FollowUpMode = project.FollowUpMode
	}
	if project.TuiMode != "" {
		m.TuiMode = project.TuiMode
	}
	if project.FullscreenExitOutput != "" {
		m.FullscreenExitOutput = project.FullscreenExitOutput
	}
	if project.FullscreenScrollbar != "" {
		m.FullscreenScrollbar = project.FullscreenScrollbar
	}
	if project.MaskSecretInput != nil {
		m.MaskSecretInput = cloneBoolPtr(project.MaskSecretInput)
	}
	if project.FullscreenWheelScrollLines != nil {
		m.FullscreenWheelScrollLines = slices.Clone(project.FullscreenWheelScrollLines)
	}
	if project.DeviceID != "" {
		m.DeviceID = project.DeviceID
	}
	m.Codemode = mergeCodemodeSettings(m.Codemode, project.Codemode)
	if project.FullscreenCopyOnSelect != nil {
		m.FullscreenCopyOnSelect = cloneBoolPtr(project.FullscreenCopyOnSelect)
	}
	if project.collapseChangelogSet {
		m.CollapseChangelog = project.CollapseChangelog
		m.collapseChangelogSet = true
	}
	if project.showCacheMissNoticesSet {
		m.ShowCacheMissNotices = project.ShowCacheMissNotices
		m.showCacheMissNoticesSet = true
	}
	if project.EnableSkillCommands != nil {
		m.EnableSkillCommands = cloneBoolPtr(project.EnableSkillCommands)
	}
	if project.EnableInstallTelemetry != nil {
		m.EnableInstallTelemetry = cloneBoolPtr(project.EnableInstallTelemetry)
	}
	if project.hideThinkingBlockSet {
		m.HideThinkingBlock = project.HideThinkingBlock
		m.hideThinkingBlockSet = true
	}
	if project.quietStartupSet {
		m.QuietStartup = project.QuietStartup
		m.quietStartupSet = true
	}
	if project.ShowImages != nil {
		m.ShowImages = cloneBoolPtr(project.ShowImages)
	}
	if project.ImageWidthCells > 0 {
		m.ImageWidthCells = project.ImageWidthCells
	}
	if project.ClearOnShrink != nil {
		m.ClearOnShrink = cloneBoolPtr(project.ClearOnShrink)
	}
	if project.ShowTerminalProgress != nil {
		m.ShowTerminalProgress = cloneBoolPtr(project.ShowTerminalProgress)
	}
	if project.terminalHyperlinks != nil {
		m.terminalHyperlinks = slices.Clone(project.terminalHyperlinks)
	}
	if project.terminalImages != nil {
		m.terminalImages = slices.Clone(project.terminalImages)
	}
	if project.terminalTrueColor != nil {
		m.terminalTrueColor = slices.Clone(project.terminalTrueColor)
	}
	if project.ImageAutoResize != nil {
		m.ImageAutoResize = cloneBoolPtr(project.ImageAutoResize)
	}
	if project.blockImagesSet {
		m.BlockImages = project.BlockImages
		m.blockImagesSet = true
	}
	if project.ThinkingBudgets != nil {
		if m.ThinkingBudgets == nil {
			m.ThinkingBudgets = &ThinkingBudgetsSettings{}
		}
		if project.ThinkingBudgets.Minimal != nil {
			m.ThinkingBudgets.Minimal = cloneIntPtr(project.ThinkingBudgets.Minimal)
		}
		if project.ThinkingBudgets.Low != nil {
			m.ThinkingBudgets.Low = cloneIntPtr(project.ThinkingBudgets.Low)
		}
		if project.ThinkingBudgets.Medium != nil {
			m.ThinkingBudgets.Medium = cloneIntPtr(project.ThinkingBudgets.Medium)
		}
		if project.ThinkingBudgets.High != nil {
			m.ThinkingBudgets.High = cloneIntPtr(project.ThinkingBudgets.High)
		}
	}
	if project.Markdown != nil {
		if m.Markdown == nil {
			m.Markdown = &MarkdownSettings{}
		}
		if project.Markdown.CodeBlockIndent != "" {
			m.Markdown.CodeBlockIndent = project.Markdown.CodeBlockIndent
		}
		if project.Markdown.Mermaid != "" {
			m.Markdown.Mermaid = project.Markdown.Mermaid
		}
	}
	if project.Warnings != nil {
		if m.Warnings == nil {
			m.Warnings = &WarningSettings{}
		}
		if project.Warnings.anthropicExtraUsageSet {
			m.Warnings.AnthropicExtraUsage = project.Warnings.AnthropicExtraUsage
			m.Warnings.anthropicExtraUsageSet = true
		}
	}
	if project.ShowHardwareCursor != nil {
		m.ShowHardwareCursor = cloneBoolPtr(project.ShowHardwareCursor)
	}
	if project.EditorPaddingX != nil {
		m.EditorPaddingX = cloneIntPtr(project.EditorPaddingX)
	}
	if project.OutputPad != nil {
		m.OutputPad = cloneIntPtr(project.OutputPad)
	}
	if project.ExternalEditor != "" {
		m.ExternalEditor = project.ExternalEditor
	}
	if project.AutocompleteMaxVisible != nil {
		m.AutocompleteMaxVisible = cloneIntPtr(project.AutocompleteMaxVisible)
	}
	return m
}
