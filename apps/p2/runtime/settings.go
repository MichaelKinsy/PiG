package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/src/runtime/settings.ts: `settings.json` of the data directory: Pi Mobile's settings plus p2's additions
// (`enabledModels`, `detail`, `ownerName`, `codemode`, `cacheWarming`).

// Settings is a validated settings.json value (Pi's `MobileSettings`): a JSON object in the file's key order, so a save
// writes what was read.
type Settings = *chordjson.Object

var (
	settingsThinkingLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}
	settingsQueueModes     = []string{"all", "one-at-a-time"}
)

// stringEnum is pi-ai's `StringEnum` ({"type":"string","enum":[...]}) with the enum as a JSON array value, which the
// validator reads (ai.StringEnum keeps the caller's []string).
func stringEnum(values []string) schema.Schema {
	members := make([]any, len(values))
	for i, value := range values {
		members[i] = value
	}
	return chordjson.ObjectOf("type", "string", "enum", members)
}

func settingsInteger(minimum float64) schema.Schema { return schema.Int(schema.Minimum(minimum)) }

// EnabledModelsSchema is the `enabledModels` patterns: `provider/modelId` or `modelId`, a `*`/`?` glob over either, with
// an optional `:thinkingLevel`, in order.
var EnabledModelsSchema = schema.Arr(schema.Str(schema.MinLength(1), schema.MaxLength(300)), schema.MaxItems(500))

// SettingsSchema is the schema of settings.json.
var SettingsSchema = schema.Obj(schema.Properties{
	schema.Opt("defaultModel", schema.Obj(schema.Properties{schema.Prop("provider", schema.Str(schema.MinLength(1))), schema.Prop("modelId", schema.Str(schema.MinLength(1)))})),
	schema.Opt("thinkingLevel", stringEnum(settingsThinkingLevels)),
	schema.Opt("enabledModels", EnabledModelsSchema),
	schema.Opt("stream", schema.Obj(schema.Properties{
		schema.Opt("transport", stringEnum([]string{"auto", "sse", "websocket"})),
		schema.Opt("timeoutMs", settingsInteger(1)),
		schema.Opt("maxRetries", settingsInteger(0)),
		schema.Opt("maxRetryDelayMs", settingsInteger(0)),
		schema.Opt("cacheRetention", stringEnum([]string{"none", "short", "long"})),
	})),
	schema.Opt("retry", schema.Obj(schema.Properties{
		schema.Opt("enabled", schema.Bool()),
		schema.Opt("maxRetries", settingsInteger(0)),
		schema.Opt("baseDelayMs", settingsInteger(0)),
		schema.Opt("maxAgentDelayMs", settingsInteger(0)),
	})),
	schema.Opt("compaction", schema.Obj(schema.Properties{
		schema.Opt("enabled", schema.Bool()),
		schema.Opt("reserveTokens", settingsInteger(1)),
		schema.Opt("keepRecentTokens", settingsInteger(1)),
		schema.Opt("backgroundTokens", settingsInteger(0)),
	})),
	schema.Opt("toolExecution", stringEnum([]string{"parallel", "sequential"})),
	schema.Opt("steeringMode", stringEnum(settingsQueueModes)),
	schema.Opt("followUpMode", stringEnum(settingsQueueModes)),
	// Offer the durable `codemode` tool (scripts that call the other tools), with the codemode plugin loaded. Default on
	// in p2 (Pi Mobile: off); `false` uninstalls it live from every worker.
	schema.Opt("codemode", schema.Bool()),
	// pi's `cacheWarming`, kept by the cache-warming plugin: "idle" (default) keeps a conversation's prompt cache entry warm
	// while its run streams and for up to 30 minutes after it settled, "streaming" only while it streams, "off" never.
	schema.Opt("cacheWarming", stringEnum([]string{"off", "streaming", "idle"})),
	// How much of a turn the web chat shows, from 1 (only the answer) to 4 (thinking in full and each tool call as a row).
	schema.Opt("detail", schema.Int(schema.Minimum(1), schema.Maximum(4))),
	// The owner's name on the entries the owner writes from now on (default "Owner"). No control characters, `<` or `>`.
	schema.Opt("ownerName", schema.Str(schema.MinLength(1), schema.MaxLength(40), schema.Pattern("^[^<>\\u0000-\\u001f\\u007f]*\\S[^<>\\u0000-\\u001f\\u007f]*$"))),
})

var isSettings = check(SettingsSchema)

// ErrInvalidSettings is a settings value that does not match SettingsSchema.
var ErrInvalidSettings = errors.New("Invalid settings.json configuration")

// ErrInvalidSettingsSyntax is a settings.json text that is not JSON.
var ErrInvalidSettingsSyntax = errors.New("Invalid settings.json syntax")

// ValidateSettings is value as settings, copied; ErrInvalidSettings when it does not match the schema.
func ValidateSettings(value any) (Settings, error) {
	if !isSettings(value) {
		return nil, ErrInvalidSettings
	}
	copied, err := chordjson.Copy(value)
	if err != nil {
		return nil, fmt.Errorf("copy settings: %w", err)
	}
	settings, _ := copied.(*chordjson.Object)
	return settings, nil
}

// ParseSettings is the settings in content (comments and a byte order mark allowed).
func ParseSettings(content string) (Settings, error) {
	value, err := chordjson.Decode([]byte(StripJSONComments(StripBOM(content))))
	if err != nil {
		return nil, ErrInvalidSettingsSyntax
	}
	return ValidateSettings(value)
}

// stringifyIndented is `JSON.stringify(value, null, 2)`.
func stringifyIndented(value any) (string, error) {
	compact, err := chordjson.Marshal(value)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, compact, "", "  "); err != nil {
		return "", err
	}
	return out.String(), nil
}

// SaveSettings validates value and writes it to path under the file's lock.
func SaveSettings(ctx context.Context, path string, value any) error {
	settings, err := ValidateSettings(value)
	if err != nil {
		return err
	}
	text, err := stringifyIndented(settings)
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	file := NewLockedFile(path)
	defer file.Close()
	_, err = WithLock(ctx, file, func(*string) (LockResult[struct{}], error) {
		return LockResult[struct{}]{Next: &text}, nil
	})
	return err
}

// StringifyIndented is `JSON.stringify(value, null, 2)`, for the configuration files p2 writes.
func StringifyIndented(value any) (string, error) { return stringifyIndented(value) }
