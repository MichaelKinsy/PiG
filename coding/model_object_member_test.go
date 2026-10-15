package coding

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// modelObjectMember is one member of the model object extensions receive for model (extension.ModelObject), as JSON
// decodes it, or nil when the object has no such member.
func modelObjectMember(t *testing.T, model ai.AnyModel, key string) any {
	t.Helper()
	object, err := extension.ModelObject(model)
	if err != nil {
		t.Fatal(err)
	}
	var members map[string]any
	if err := json.Unmarshal(object, &members); err != nil {
		t.Fatal(err)
	}
	return members[key]
}

// modelObjectMembersOf decodes the members of a model object.
func modelObjectMembersOf(t *testing.T, object json.RawMessage) map[string]any {
	t.Helper()
	var members map[string]any
	if err := json.Unmarshal(object, &members); err != nil {
		t.Fatalf("model object %s: %v", object, err)
	}
	return members
}
