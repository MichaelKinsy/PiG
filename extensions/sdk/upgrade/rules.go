// SPDX-License-Identifier: MIT

// pig additive (D109): Pi has no SDK to drift from; see docs/additive-features.md.

// Package upgrade rewrites Go extensions written for an older SDK so they build
// against this one. Each rule is one breaking SDK change: the rules sit beside
// the SDK so a breaking change and its rewrite land together, and the
// apidiff gate (automation/ci/sdkapidiff) fails a break that has no rule.
//
// A rule can classify a compiler diagnostic ([Classify]), name the change with
// its old and new shape, and, when the change has one correct mechanical form,
// rewrite the source ([Plan]). The rewrites read the extension's types and edit
// its syntax tree; they never match source text.
package upgrade

import "strings"

// SDKModulePath is the import path of the SDK, and LegacySDKModulePath the
// path it carried in earlier releases. An extension may import either.
const (
	SDKModulePath       = "github.com/MichaelKinsy/PiG/extensions/sdk"
	LegacySDKModulePath = "github.com/mainstai/pig/extensions/sdk"
)

// IsSDKPath reports whether path is an SDK import path.
func IsSDKPath(path string) bool { return path == SDKModulePath || path == LegacySDKModulePath }

// Change is one breaking change to a public SDK symbol.
type Change struct {
	// Symbol is the name apidiff reports, such as "Context.GetSessionID".
	Symbol string
	// Old and New are the shapes of the symbol before and after the change.
	Old, New string
	// Since is the SDK release that introduced the new shape.
	Since string
}

// Rule is one breaking SDK change and its remedy.
type Rule struct {
	// ID names the rule.
	ID string
	// Summary is one line naming the change.
	Summary string
	// Rewrites reports whether Plan rewrites the change. A rule that does not
	// rewrite classifies the diagnostic and names the manual remedy.
	Rewrites bool
	// Remedy tells the author what to do when the rule does not rewrite the use.
	Remedy string
	// Changes are the SDK symbols the rule covers.
	Changes []Change
}

// Rule IDs.
const (
	RuleContextGetters      = "context-getters-return-error"
	RuleOptionalBool        = "optional-bool-pointer"
	RuleContextUsage        = "context-usage-nullable"
	RuleEditorComponent     = "set-editor-component-factory"
	RuleConstrainedSampling = "constrained-sampling-union"
	RuleAutocompleteFactory = "autocomplete-provider-factory"
	RuleNotComparable       = "struct-not-comparable"
	RuleTransformHeaders    = "provider-stream-transform-headers-removed"
	RuleProviderCredential  = "provider-credential-ordered"
	RuleSystemPromptGetter  = "before-agent-start-system-prompt-getter"
	RuleReadonlySession     = "context-session-manager-readonly"
	RuleModelRegistryFind   = "model-registry-find-returns-error"
	RuleDialogResults       = "dialog-results-are-json-values"
)

// getter is one Context method that gained an error result. Params is the
// parameter list and Old and New are the result lists, as Go writes them.
type getter struct {
	Name, Params, Old, New string
	// Pointer is set when the old string result became *string: Pi's undefined
	// is a nil pointer, and the old SDK returned the empty string for it.
	Pointer bool
	// Since is the SDK release that gave the getter its error result; empty means 0.3.0.
	Since string
	// MustHandle is set when the old getter could not fail. The old host-backed
	// getters returned their zero value on a failed host call, so a use whose
	// function cannot return the error keeps that behavior by discarding it. The
	// old ConfigHome had no failure to discard: dropping its error would turn a
	// missing home directory into paths relative to the working directory, so
	// such a use is listed for the author instead.
	MustHandle bool
}

func (g getter) shape(results string) string { return "func(" + g.Params + ") " + results }

var getters = []getter{
	{Name: "ConfigHome", Old: "string", New: "(string, error)", Since: "0.5.0", MustHandle: true},
	{Name: "GetActiveTools", Old: "[]string", New: "([]string, error)"},
	{Name: "GetAllThemes", Old: "[]ThemeMeta", New: "([]ThemeMeta, error)"},
	{Name: "GetAllTools", Old: "[]ToolInfo", New: "([]ToolInfo, error)"},
	{Name: "GetBranch", Old: "[]BranchEntry", New: "([]BranchEntry, error)"},
	{Name: "GetCommands", Old: "[]CommandInfo", New: "([]CommandInfo, error)"},
	{Name: "GetContextUsage", Old: "*ContextUsage", New: "(*ContextUsage, error)"},
	{Name: "GetEditorText", Old: "string", New: "(string, error)"},
	{Name: "GetEntries", Old: "[]json.RawMessage", New: "([]json.RawMessage, error)"},
	{Name: "GetFlag", Params: "string", Old: "any", New: "(any, error)"},
	{Name: "GetLeafID", Old: "string", New: "(*string, error)", Pointer: true},
	{Name: "GetModelAuth", Params: "string, string", Old: "json.RawMessage", New: "(json.RawMessage, error)"},
	{Name: "GetModelInfo", Old: "*ModelInfo", New: "(*ModelInfo, error)"},
	{Name: "GetSessionFile", Old: "string", New: "(*string, error)", Pointer: true},
	{Name: "GetSessionID", Old: "string", New: "(string, error)"},
	{Name: "GetSessionName", Old: "string", New: "(*string, error)", Pointer: true},
	{Name: "GetSystemPrompt", Old: "string", New: "(string, error)"},
	{Name: "GetSystemPromptOptions", Old: "SystemPromptOptions", New: "(SystemPromptOptions, error)"},
	{Name: "GetThinkingLevel", Old: "string", New: "(string, error)"},
	{Name: "GetToolsExpanded", Old: "bool", New: "(bool, error)"},
	{Name: "HasPendingMessages", Old: "bool", New: "(bool, error)"},
	{Name: "IsIdle", Old: "bool", New: "(bool, error)"},
	{Name: "IsProjectTrusted", Old: "bool", New: "(bool, error)"},
}

func getterByName(name string) (getter, bool) {
	for _, candidate := range getters {
		if candidate.Name == name {
			return candidate, true
		}
	}
	return getter{}, false
}

func getterChanges() []Change {
	changes := make([]Change, len(getters))
	for i, g := range getters {
		since := g.Since
		if since == "" {
			since = "0.3.0"
		}
		changes[i] = Change{Symbol: "Context." + g.Name, Old: g.shape(g.Old), New: g.shape(g.New), Since: since}
	}
	return changes
}

// Rules returns every rule, in a stable order.
func Rules() []Rule {
	return []Rule{
		{
			ID:       RuleContextGetters,
			Summary:  "Context getters return an error as their last result",
			Rewrites: true,
			Remedy:   "Receive the error and return it from the handler: `id, err := ctx.GetSessionID()`.",
			Changes:  getterChanges(),
		},
		{
			ID:       RuleOptionalBool,
			Summary:  "optional boolean options are *bool",
			Rewrites: true,
			Remedy:   "Wrap the value in sdk.Bool: `TriggerTurn: sdk.Bool(true)`. Leave the field nil to take the host default.",
			Changes: []Change{
				{Symbol: "SendMessageOptions.TriggerTurn", Old: "bool", New: "*bool", Since: "0.2.0"},
			},
		},
		{
			ID:       RuleContextUsage,
			Summary:  "ContextUsage.Tokens and Percent are nil when unknown",
			Rewrites: true,
			Remedy:   "Read the value with `usage.TokensOr(0)` or `usage.PercentOr(0)`, or check the pointer for nil.",
			Changes: []Change{
				{Symbol: "ContextUsage.Tokens", Old: "int", New: "*int", Since: "0.3.0"},
				{Symbol: "ContextUsage.Percent", Old: "float64", New: "*float64", Since: "0.3.0"},
			},
		},
		{
			ID:      RuleEditorComponent,
			Summary: "SetEditorComponent takes an EditorFactory",
			Remedy:  "Pass an sdk.EditorFactory, which returns an EditorComponent that embeds the Editor it receives, or nil to restore the host's editor.",
			Changes: []Change{
				{Symbol: "Context.SetEditorComponent", Old: "func(any) error", New: "func(EditorFactory) error", Since: "0.4.2"},
			},
		},
		{
			ID:      RuleConstrainedSampling,
			Summary: "ToolWithConstrainedSampling takes the ToolConstrainedSampling union",
			Remedy:  "Pass an sdk.ConstrainedSampling value or pointer, sdk.DisabledConstrainedSampling{} for Pi's explicit false, or nil.",
			Changes: []Change{
				{Symbol: "(*Extension).ToolWithConstrainedSampling", Old: "func(string, string, Schema, ConstrainedSampling, ToolFunc)", New: "func(string, string, Schema, ToolConstrainedSampling, ToolFunc)", Since: "0.3.0"},
			},
		},
		{
			ID:      RuleAutocompleteFactory,
			Summary: "AutocompleteProviderFactory is a function type",
			Remedy:  "Pass a func(sdk.Context, *sdk.AutocompleteProvider) (*sdk.AutocompleteProvider, error).",
			Changes: []Change{
				{Symbol: "AutocompleteProviderFactory", Old: "= any", New: "func(Context, *AutocompleteProvider) (*AutocompleteProvider, error)", Since: "0.3.0"},
				{Symbol: "Context.AddAutocompleteProvider", Old: "func(any) error", New: "func(AutocompleteProviderFactory) error", Since: "0.3.0"},
			},
		},
		{
			ID:      RuleNotComparable,
			Summary: "ToolInfo, OAuthCredentials, RemoteOverlayOptions and Editor are no longer comparable",
			Remedy:  "Compare the fields you need, or use reflect.DeepEqual. Do not use these types as map keys or compare them with ==.",
			Changes: []Change{
				{Symbol: "ToolInfo", Old: "comparable struct", New: "struct with a slice and a raw message", Since: "0.3.0"},
				{Symbol: "OAuthCredentials", Old: "comparable struct", New: "struct that is not comparable", Since: "0.3.0"},
				{Symbol: "RemoteOverlayOptions", Old: "comparable struct", New: "struct with a function field (OnHandle) and a pointer to OverlayOptions", Since: "0.5.0"},
				{Symbol: "Editor", Old: "comparable struct", New: "struct with the handler table of CustomEditor's actions (hooks)", Since: "0.5.2"},
			},
		},
		{
			ID:      RuleTransformHeaders,
			Summary: "ProviderStreamOptions has no TransformHeaders",
			Remedy:  "Delete the use. Pi's model runtime applies transformHeaders before it calls Provider.stream and never passes it on, so the host never set it: the headers in the stream options are already transformed.",
			Changes: []Change{
				{Symbol: "ProviderStreamOptions.TransformHeaders", Old: "func(map[string]string) (map[string]string, error)", New: "removed", Since: "0.5.0"},
			},
		},
		{
			ID:      RuleProviderCredential,
			Summary: "a provider object's credential is *Credential, which keeps its key order",
			Remedy:  "Build a credential with sdk.NewCredential(\"type\", \"api_key\", \"key\", key), read it with Get, String and Keys, and refresh it with Clone and Set. OAuthAuth.Login also receives the app's LoginOptions.",
			Changes: []Change{
				{Symbol: "APIKeyAuth.Login", Old: "func(AuthInteraction) (map[string]any, error)", New: "func(AuthInteraction) (*Credential, error)", Since: "0.5.0"},
				{Symbol: "OAuthAuth.Login", Old: "func(AuthInteraction) (map[string]any, error)", New: "func(AuthInteraction, LoginOptions) (*Credential, error)", Since: "0.5.0"},
				{Symbol: "OAuthAuth.Refresh", Old: "func(map[string]any, context.Context) (map[string]any, error)", New: "func(*Credential, context.Context) (*Credential, error)", Since: "0.5.0"},
				{Symbol: "OAuthAuth.ToAuth", Old: "func(map[string]any) (map[string]any, error)", New: "func(*Credential) (map[string]any, error)", Since: "0.5.0"},
				{Symbol: "APIKeyAuthInput.Credential", Old: "map[string]any", New: "*Credential", Since: "0.5.0"},
				{Symbol: "RefreshModelsContext.Credential", Old: "map[string]any", New: "*Credential", Since: "0.5.0"},
				{Symbol: "Provider.FilterModels", Old: "func([]map[string]any, map[string]any) ([]map[string]any, error)", New: "func([]map[string]any, *Credential) ([]map[string]any, error)", Since: "0.5.0"},
			},
		},
		{
			ID:      RuleDialogResults,
			Summary: "Select, Input, Editor and Confirm return the JSON value the client answered with, as an any",
			Remedy:  "Assert the type you expect: `answer, ok, err := ctx.Select(...)` then `choice, _ := answer.(string)`, and `confirmed, _ := answer.(bool)` for Confirm. Pi's RPC mode passes a client's JSON through unchecked, so the answer can be a number, nil, a map or a slice. A cancelled dialog is nil where it was the empty string.",
			Changes: dialogResultChanges(),
		},
		{
			ID:       RuleSystemPromptGetter,
			Summary:  `before_agent_start's data["systemPrompt"] is an *SystemPrompt that renders the handler's current options`,
			Rewrites: true,
			Remedy:   "Read the prompt with `data[\"systemPrompt\"].(*sdk.SystemPrompt).Get()`, which returns `(string, error)`, and return the error from the handler.",
			Changes: []Change{
				{Symbol: systemPromptSymbol, Old: "string", New: "*SystemPrompt", Since: systemPromptSince},
			},
		},
		{
			ID:      RuleReadonlySession,
			Summary: "ctx.SessionManager is Pi's ReadonlySessionManager; SessionManager is the class an extension constructs",
			Remedy:  "Name the type sdk.ReadonlySessionManager where you named sdk.SessionManager for the value of ctx.SessionManager(); the calls through ctx.SessionManager() do not change. To own a session log, construct sdk.SessionManager with SessionManagerCreate, SessionManagerOpen or SessionManagerInMemory.",
			Changes: []Change{
				{Symbol: "SessionManager", Old: "the read-only view of the host's session log (the type of ctx.SessionManager())", New: "Pi's SessionManager class, which an extension constructs", Since: "0.5.2"},
				{Symbol: "SetupSessionManager.SessionManager", Old: "field of SetupSessionManager", New: "removed", Since: "0.5.2"},
			},
		},
		{
			ID:      RuleModelRegistryFind,
			Summary: "ModelRegistry.Find returns an error as its last result",
			Remedy:  "Receive the error and return it from the handler: `model, err := ctx.ModelRegistry().Find(provider, id)`. A nil model with a nil error means no model matches; a host or transport failure is the error.",
			Changes: []Change{
				{Symbol: "ModelRegistry.Find", Old: "func(string, string) map[string]any", New: "func(string, string) (map[string]any, error)", Since: "0.5.2"},
			},
		},
	}
}

func dialogResultChanges() []Change {
	var changes []Change
	for _, name := range []string{"Select", "SelectWithOptions", "Input", "InputWithOptions", "Editor"} {
		changes = append(changes, Change{Symbol: "Context." + name, Old: "func(...) (string, bool, error)", New: "func(...) (any, bool, error)", Since: dialogResultsSince})
	}
	for _, name := range []string{"Confirm", "ConfirmWithOptions"} {
		changes = append(changes, Change{Symbol: "Context." + name, Old: "func(...) (bool, error)", New: "func(...) (any, error)", Since: dialogResultsSince})
	}
	return changes
}

// dialogResultsSince is the SDK release whose dialogs return JSON values.
const dialogResultsSince = "0.5.2"

// RuleByID returns the rule with the given ID.
func RuleByID(id string) (Rule, bool) {
	for _, rule := range Rules() {
		if rule.ID == id {
			return rule, true
		}
	}
	return Rule{}, false
}

// Covers reports whether a rule covers the symbol an apidiff report names.
// apidiff writes a method as "Context.GetSessionID" or "(*Extension).Tool".
func Covers(symbol string) bool {
	_, ok := changeFor(symbol)
	return ok
}

func changeFor(symbol string) (Change, bool) {
	symbol = strings.TrimSpace(symbol)
	for _, rule := range Rules() {
		for _, change := range rule.Changes {
			if change.Symbol == symbol {
				return change, true
			}
		}
	}
	return Change{}, false
}
