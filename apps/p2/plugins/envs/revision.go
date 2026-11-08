package envs

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// compareUTF16 orders strings by UTF-16 code unit, as JavaScript's `<` does.
func compareUTF16(left, right string) int {
	return slices.Compare(utf16.Encode([]rune(left)), utf16.Encode([]rune(right)))
}

// canonical is src/runtime/environments.ts' canonical: an object's fields sorted by key, recursively through objects (an
// array and what is in it stays as it is).
func canonical(value any) any {
	object, ok := value.(*chordjson.Object)
	if !ok || object == nil {
		return value
	}
	keys := object.Keys()
	slices.SortStableFunc(keys, compareUTF16)
	sorted := chordjson.NewObject(len(keys))
	for _, key := range keys {
		sorted.Set(key, canonical(object.Value(key)))
	}
	return sorted
}

// EnvRevision changes whenever the configuration of an env changes (envRevision of @p2/app/v1/node).
func EnvRevision(config *chordjson.Object) string {
	text, err := chordjson.Marshal(canonical(config))
	if err != nil {
		// A validated configuration is strict JSON.
		panic(err)
	}
	sum := sha256.Sum256(text)
	return hex.EncodeToString(sum[:])[:12]
}

// ConnectionRevision is the revision a connection to environment name was opened for: its own configuration's, and for a
// sandbox also its host's (the connection goes through the host), so editing either starts a new connection. An unknown
// name has no revision ("").
func ConnectionRevision(configs *chordjson.Object, name string) string {
	config, ok := configs.Value(name).(*chordjson.Object)
	if !ok {
		return ""
	}
	if config.Value("type") == "ssh" {
		return EnvRevision(config)
	}
	hostName, _ := config.Value("host").(string)
	host, _ := configs.Value(hostName).(*chordjson.Object)
	if host == nil {
		return EnvRevision(config) + "+"
	}
	return EnvRevision(config) + "+" + EnvRevision(host)
}
