package extension

// FlagType is the value type of a CLI flag registered by an extension.
// Mirrors upstream's "boolean" | "string" union.
type FlagType string

const (
	FlagBoolean FlagType = "boolean"
	FlagString  FlagType = "string"
)

// FlagOptions is the registration payload for [API.RegisterFlag]. Mirrors
// the inline option object on upstream ExtensionAPI.registerFlag.
type FlagOptions struct {
	Description string   `json:"description,omitempty"`
	Type        FlagType `json:"type"`
	// Default is bool when Type == FlagBoolean and string when
	// Type == FlagString. Nil when no default.
	Default any `json:"default,omitempty"`
}

// ExtensionFlag mirrors upstream's ExtensionFlag: the loader's view of a
// registered flag, including its source extension. Hosts hand this to the
// CLI parser.
type ExtensionFlag struct {
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	Type          FlagType `json:"type"`
	Default       any      `json:"default,omitempty"`
	ExtensionPath string   `json:"extensionPath"`
}
