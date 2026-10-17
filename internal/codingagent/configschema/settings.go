package configschema

import "github.com/MichaelKinsy/PiG/ai"

// Ports packages/coding-agent/src/core/settings-schema.ts and settings-defaults.ts.

// settingsDefaults is SETTINGS_DEFAULTS, the defaults the published JSON Schema carries.
var settingsDefaults = struct {
	transport, sessionShareDestination, steeringMode, followUpMode                        string
	compactionEnabled                                                                     bool
	compactionReserveTokens, compactionKeepRecentTokens                                   int
	branchSummaryReserveTokens                                                            int
	branchSummarySkipPrompt                                                               bool
	retryEnabled                                                                          bool
	retryMaxRetries, retryBaseDelayMs, retryMaxAgentDelayMs, retryProviderMaxRetryDelayMs int
	hideThinkingBlock, showCacheMissNotices, quietStartup                                 bool
	defaultProjectTrust                                                                   string
	collapseChangelog, enableInstallTelemetry, enableAnalytics, enableSkillCommands       bool
	terminalShowImages                                                                    bool
	terminalImageWidthCells                                                               int
	terminalClearOnShrink, terminalShowTerminalProgress                                   bool
	imagesAutoResize, imagesBlockImages                                                   bool
	doubleEscapeAction, treeFilterMode                                                    string
	editorPaddingX, outputPad, autocompleteMaxVisible                                     int
	markdownCodeBlockIndent, markdownMermaid                                              string
	warningsAnthropicExtraUsage                                                           bool
	codemodeMode                                                                          string
	codemodeInlineBudget                                                                  int
	cacheWarming, tuiMode, fullscreenExitOutput, fullscreenScrollbar                      string
	fullscreenCopyOnSelect                                                                bool
	fullscreenWheelScrollLines                                                            string
}{
	transport: "auto", sessionShareDestination: "auto", steeringMode: "one-at-a-time", followUpMode: "one-at-a-time",
	compactionEnabled: true, compactionReserveTokens: 16384, compactionKeepRecentTokens: 20000,
	branchSummaryReserveTokens: 16384,
	retryEnabled:               true, retryMaxRetries: 3, retryBaseDelayMs: 2000, retryMaxAgentDelayMs: ai.DefaultMaxAgentRetryDelayMs, retryProviderMaxRetryDelayMs: 60000,
	defaultProjectTrust: "ask", enableInstallTelemetry: true, enableSkillCommands: true,
	terminalShowImages: true, terminalImageWidthCells: 60,
	imagesAutoResize:   true,
	doubleEscapeAction: "tree", treeFilterMode: "default",
	outputPad: 1, autocompleteMaxVisible: 5,
	markdownCodeBlockIndent: "  ", markdownMermaid: "streaming",
	warningsAnthropicExtraUsage: true,
	codemodeMode:                "on", codemodeInlineBudget: 3000,
	cacheWarming: "streaming", tuiMode: "fullscreen", fullscreenExitOutput: "transcript", fullscreenScrollbar: "auto",
	fullscreenCopyOnSelect: true, fullscreenWheelScrollLines: "auto",
}

// nonNegativeSafeInteger is Type.Integer({ minimum: 0, maximum: Number.MAX_SAFE_INTEGER }).
func nonNegativeSafeInteger(opts ...Opt) *Schema {
	return Integer(append([]Opt{{"minimum", 0}, {"maximum", int64(1<<53 - 1)}}, opts...)...)
}

func timeoutSetting(opts ...Opt) *Schema {
	return Union([]*Schema{Number(Opt{"minimum", 0}), Literal("disabled")}, opts...)
}

func thinkingBudgetsSettings(opts ...Opt) *Schema {
	return Object([]Prop{Optional("minimal", Number()), Optional("low", Number()), Optional("medium", Number()), Optional("high", Number())}, opts...)
}

func codemodeMode(opts ...Opt) *Schema { return Union(literals("on", "only"), opts...) }

func stringArray(opts ...Opt) *Schema { return Array(String(), opts...) }

func settingsSchema() *Schema {
	d := settingsDefaults
	compactionModelOverride := Object([]Prop{Optional("reserveTokens", nonNegativeSafeInteger()), Optional("keepRecentTokens", nonNegativeSafeInteger())})
	compaction := Object([]Prop{
		Optional("enabled", Boolean(Default(d.compactionEnabled))),
		Optional("reserveTokens", nonNegativeSafeInteger(Default(d.compactionReserveTokens))),
		Optional("keepRecentTokens", nonNegativeSafeInteger(Default(d.compactionKeepRecentTokens))),
		Optional("modelOverrides", Record(compactionModelOverride, D(`Per-model overrides keyed by exact "provider/modelId" strings.`))),
	})
	branchSummary := Object([]Prop{
		Optional("reserveTokens", Number(D("Tokens reserved for the prompt and LLM response."), Default(d.branchSummaryReserveTokens))),
		Optional("skipPrompt", Boolean(D(`When true, skips the "Summarize branch?" prompt and defaults to no summary.`), Default(d.branchSummarySkipPrompt))),
	})
	providerRetry := Object([]Prop{
		Optional("timeoutMs", Number(D("SDK or provider request timeout in milliseconds."))),
		Optional("maxRetries", Number(D("SDK or provider retry attempts."))),
		Optional("maxRetryDelayMs", Number(D("Maximum server-requested delay before failing."), Default(d.retryProviderMaxRetryDelayMs))),
	})
	retry := Object([]Prop{
		Optional("enabled", Boolean(Default(d.retryEnabled))),
		Optional("maxRetries", Number(Default(d.retryMaxRetries))),
		Optional("baseDelayMs", Number(D("Exponential backoff base delay in milliseconds: 2s, 4s, 8s."), Default(d.retryBaseDelayMs))),
		Optional("maxAgentDelayMs", Number(Default(d.retryMaxAgentDelayMs))),
		Optional("provider", providerRetry),
		Optional("maxDelayMs", Number(D("Legacy retry delay setting. Use provider.maxRetryDelayMs instead."), Opt{"deprecated", true})),
	})
	terminal := Object([]Prop{
		Optional("showImages", Boolean(D("Show images when the terminal supports them."), Default(d.terminalShowImages))),
		Optional("imageWidthCells", Number(D("Preferred inline image width in terminal cells."), Default(d.terminalImageWidthCells))),
		Optional("clearOnShrink", Boolean(D("Clear empty rows when content shrinks."), Default(d.terminalClearOnShrink))),
		Optional("showTerminalProgress", Boolean(D("Show OSC 9;4 terminal progress indicators."), Default(d.terminalShowTerminalProgress))),
		Optional("hyperlinks", Union([]*Schema{Boolean(), Literal("auto")})),
		Optional("images", Union([]*Schema{Literal("kitty"), Literal("iterm2"), Literal("auto"), Literal(false)})),
		Optional("trueColor", Union([]*Schema{Boolean(), Literal("auto")})),
	})
	images := Object([]Prop{
		Optional("autoResize", Boolean(D("Resize images to 2000x2000 maximum for better model compatibility."), Default(d.imagesAutoResize))),
		Optional("blockImages", Boolean(D("When true, prevents all images from being sent to LLM providers."), Default(d.imagesBlockImages))),
	})
	markdown := Object([]Prop{
		Optional("codeBlockIndent", String(Default(d.markdownCodeBlockIndent))),
		Optional("mermaid", Union(literals("off", "final", "streaming"), Default(d.markdownMermaid))),
	})
	warnings := Object([]Prop{Optional("anthropicExtraUsage", Boolean(Default(d.warningsAnthropicExtraUsage)))})
	codemode := Object([]Prop{
		Optional("mode", codemodeMode(D(`How codemode presents tools. "on" keeps direct tools declared and annotates tools callable from scripts; codemode lists only tools without direct exposure. "only" lists every script-callable tool in codemode and does not declare active direct tools.`), Default(d.codemodeMode))),
		Optional("inlineBudget", Number(Opt{"minimum", 0}, D("Estimated tokens available for inline codemode tool declarations."), Default(d.codemodeInlineBudget))),
	})
	packageSource := Union([]*Schema{
		String(D("Load all resources from the package.")),
		Object([]Prop{
			P("source", String()),
			Optional("autoload", Boolean(D("When false, start empty and only apply explicit resource patterns."))),
			Optional("extensions", stringArray()),
			Optional("skills", stringArray()),
			Optional("prompts", stringArray()),
			Optional("themes", stringArray()),
		}),
	}, D("Package source for npm or git packages. Use the string form to load all resources or the object form to filter resources."))
	skillsInput := Union([]*Schema{
		stringArray(),
		Object([]Prop{Optional("enableSkillCommands", Boolean()), Optional("customDirectories", stringArray())}, Opt{"deprecated", true}),
	}, D("Local skill file paths or directories."))
	return Object([]Prop{
		Optional("$schema", String(D("JSON Schema reference."))),
		Optional("lastChangelogVersion", String()),
		Optional("defaultProvider", String()),
		Optional("defaultModel", String()),
		Optional("defaultThinkingLevel", ModelThinkingLevelSchema()),
		Optional("modelThinkingLevels", Record(ModelThinkingLevelSchema(), D(`Per-model default thinking level overrides keyed by "provider/modelId".`))),
		Optional("transport", Union(literals("auto", "sse", "websocket", "websocket-cached"), Default(d.transport))),
		Optional("sessionShareDestination", Union(literals("auto", "gist", "radius"), D(`Destination used by /share. "auto" uses Radius when authenticated and otherwise creates a GitHub gist.`), Default(d.sessionShareDestination))),
		Optional("steeringMode", Union(literals("all", "one-at-a-time"), Default(d.steeringMode))),
		Optional("followUpMode", Union(literals("all", "one-at-a-time"), Default(d.followUpMode))),
		Optional("theme", String()),
		Optional("compaction", compaction),
		Optional("branchSummary", branchSummary),
		Optional("retry", retry),
		Optional("hideThinkingBlock", Boolean(Default(d.hideThinkingBlock))),
		Optional("showCacheMissNotices", Boolean(D("Show cache cost and provider recovery notices."), Default(d.showCacheMissNotices))),
		Optional("externalEditor", String(D("Command for Ctrl+G external editor; takes precedence over VISUAL and EDITOR."))),
		Optional("shellPath", String(D("Custom shell path, for example for Cygwin on Windows, with support for leading ~ expansion."))),
		Optional("quietStartup", Union([]*Schema{Boolean(), Literal("header")}, D(`When true, hide all startup output. When "header", keep only the startup header.`), Default(d.quietStartup))),
		Optional("defaultProjectTrust", Union(literals("ask", "always", "never"), D("Global setting only."), Default(d.defaultProjectTrust))),
		Optional("shellCommandPrefix", String(D("Prefix prepended to every bash command, for example to enable shell aliases."))),
		Optional("npmCommand", stringArray(D(`Command used for npm package lookup and installation, in argv form such as ["mise", "exec", "node@20", "--", "npm"].`))),
		Optional("collapseChangelog", Boolean(D("Show the condensed changelog after update; use /changelog for the full changelog."), Default(d.collapseChangelog))),
		Optional("enableInstallTelemetry", Boolean(D("Send an anonymous version and update ping after changelog-detected updates."), Default(d.enableInstallTelemetry))),
		Optional("enableAnalytics", Boolean(D("Opt in to analytics data sharing."), Default(d.enableAnalytics))),
		Optional("trackingId", String(D("Analytics tracking identifier, generated when analytics is enabled."))),
		Optional("packages", Array(packageSource, D("npm or git package sources, as strings or objects with resource filtering."))),
		Optional("extensions", stringArray(D("Local extension file paths or directories."))),
		Optional("deviceId", String(D("Stable installation UUID, generated when authentication first needs it. Global only."))),
		Optional("skills", skillsInput),
		Optional("prompts", stringArray(D("Local prompt template file paths or directories."))),
		Optional("themes", stringArray(D("Local theme file paths or directories."))),
		Optional("enableSkillCommands", Boolean(D("Register skills as /skill:name commands."), Default(d.enableSkillCommands))),
		Optional("terminal", terminal),
		Optional("images", images),
		Optional("enabledModels", stringArray(D("Model patterns for cycling, in the same format as the --models CLI flag."))),
		Optional("defaultTools", stringArray(D("Initial tool selection. Plain names replace the inherited selection; +name and -name entries add or remove tools."))),
		Optional("doubleEscapeAction", Union(literals("fork", "tree", "none"), D("Action for double-escape with an empty editor."), Default(d.doubleEscapeAction))),
		Optional("treeFilterMode", Union(literals("default", "no-tools", "user-only", "labeled-only", "all"), D("Default filter when opening /tree."), Default(d.treeFilterMode))),
		Optional("thinkingBudgets", thinkingBudgetsSettings(D("Custom token budgets for thinking levels."))),
		Optional("editorPaddingX", Number(D("Horizontal padding for the input editor."), Default(d.editorPaddingX))),
		Optional("outputPad", Union([]*Schema{Literal(0), Literal(1)}, D("Horizontal padding for transcript content."), Default(d.outputPad))),
		Optional("autocompleteMaxVisible", Number(D("Maximum visible items in the autocomplete dropdown."), Default(d.autocompleteMaxVisible))),
		Optional("showHardwareCursor", Boolean(D("Show the terminal cursor while still positioning it for IME."))),
		Optional("markdown", markdown),
		Optional("warnings", warnings),
		Optional("codemode", codemode),
		Optional("sessionDir", String(D("Custom session storage directory, in the same format as the --session-dir CLI flag."))),
		Optional("httpProxy", String(D("Proxy URL applied as HTTP_PROXY and HTTPS_PROXY for Pi-managed HTTP clients."))),
		Optional("httpIdleTimeoutMs", timeoutSetting(D(`HTTP header or body idle timeout in milliseconds; 0 or "disabled" disables it.`))),
		Optional("cacheWarming", Union(literals("off", "streaming", "idle"), D(`Cache-warming profile. "idle" also warms between agent runs. Global only because each refresh costs money.`), Default(d.cacheWarming))),
		Optional("websocketConnectTimeoutMs", timeoutSetting(D(`WebSocket connect or open handshake timeout in milliseconds; 0 or "disabled" disables it.`))),
		Optional("tuiMode", Union(literals("regular", "fullscreen"), Default(d.tuiMode))),
		Optional("fullscreenExitOutput", Union(literals("transcript", "resume-hint"), D("No effect in regular TUI mode."), Default(d.fullscreenExitOutput))),
		Optional("fullscreenScrollbar", Union(literals("auto", "always", "hidden"), D("No effect in regular TUI mode."), Default(d.fullscreenScrollbar))),
		Optional("fullscreenCopyOnSelect", Boolean(D("No effect in regular TUI mode."), Default(d.fullscreenCopyOnSelect))),
		Optional("fullscreenWheelScrollLines", Union([]*Schema{Number(), Literal("auto")}, D("Lines scrolled per wheel event in fullscreen mode; numeric values are clamped from 1 to 100."), Default(d.fullscreenWheelScrollLines))),
		Optional("queueMode", Union(literals("all", "one-at-a-time"), D("Legacy setting migrated to steeringMode."), Opt{"deprecated", true})),
		Optional("websockets", Boolean(D("Legacy setting migrated to transport."), Opt{"deprecated", true})),
	}, Opt{"additionalProperties", true})
}

func settingsArtifact() Artifact {
	return Artifact{Path: "schemas/settings.schema.json", Schema: settingsSchema(), Definitions: []Definition{{"ModelThinkingLevel", ModelThinkingLevelSchema()}}}
}
