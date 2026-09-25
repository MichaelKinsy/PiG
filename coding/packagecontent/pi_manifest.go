package packagecontent

import (
	"bytes"
	"encoding/json"
	"os"
)

// PiManifest mirrors upstream pi-manifest.ts PiManifest: the resource lists a
// package.json "pi" object declares. A nil field was absent or malformed.
type PiManifest struct {
	Extensions []string
	Skills     []string
	Prompts    []string
	Themes     []string
}

// utf8BOM is the byte-order mark upstream stripBom removes before JSON.parse.
var utf8BOM = []byte("\xef\xbb\xbf")

// ReadPiManifest mirrors upstream readPiManifest. It returns nil when the file
// cannot be read or parsed, or when the package or its "pi" value is not a JSON
// object. A resource field is kept only when it is an array of strings.
func ReadPiManifest(packageJSONPath string) *PiManifest {
	data, err := os.ReadFile(packageJSONPath)
	if err != nil {
		return nil
	}
	pkg := decodeJSONObject(bytes.TrimPrefix(data, utf8BOM))
	if pkg == nil {
		return nil
	}
	pi := decodeJSONObject(pkg["pi"])
	if pi == nil {
		return nil
	}
	manifest := &PiManifest{}
	for field, target := range map[string]*[]string{
		"extensions": &manifest.Extensions,
		"skills":     &manifest.Skills,
		"prompts":    &manifest.Prompts,
		"themes":     &manifest.Themes,
	} {
		if entries, ok := decodeJSONStringArray(pi[field]); ok {
			*target = entries
		}
	}
	return manifest
}

// decodeJSONObject returns the members of a JSON object, or nil for any other
// value, including null and arrays.
func decodeJSONObject(raw []byte) map[string]json.RawMessage {
	var object map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &object) != nil {
		return nil
	}
	return object
}

// decodeJSONStringArray reports whether raw is a JSON array whose every entry
// is a string, matching upstream Array.isArray(entries) && entries.every(string).
func decodeJSONStringArray(raw []byte) ([]string, bool) {
	var values []any
	if len(raw) == 0 || json.Unmarshal(raw, &values) != nil || values == nil {
		return nil, false
	}
	entries := make([]string, 0, len(values))
	for _, value := range values {
		entry, ok := value.(string)
		if !ok {
			return nil, false
		}
		entries = append(entries, entry)
	}
	return entries, true
}
