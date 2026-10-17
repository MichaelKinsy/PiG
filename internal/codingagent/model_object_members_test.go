package codingagent

import (
	"encoding/json"
	"testing"
)

// modelObjectMembers decodes the members of a model object extensions receive (extension.ModelObject).
func modelObjectMembers(t testing.TB, object json.RawMessage) map[string]any {
	t.Helper()
	var members map[string]any
	if err := json.Unmarshal(object, &members); err != nil {
		t.Fatalf("model object %s: %v", object, err)
	}
	return members
}
