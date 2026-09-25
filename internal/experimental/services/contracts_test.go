package services

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// The upstream services/models.ts and services/sessions.ts declarations are the wire denominator.
func TestServiceContracts(t *testing.T) {
	for _, test := range []struct {
		name  string
		value any
		want  string
	}{
		{"model", ModelRef{Provider: "local", ModelId: "a"}, `{"provider":"local","modelId":"a"}`},
		{"summary", ModelSummary{ModelRef: ModelRef{Provider: "local", ModelId: "a"}, Name: "A", Reasoning: true}, `{"provider":"local","modelId":"a","name":"A","reasoning":true}`},
		{"initial", ModelsState{Catalog: ModelsCatalog{AvailableModels: []ModelSummary{}}, Configuration: ModelsConfiguration{ThinkingLevel: ai.ThinkingOff}, Refresh: ModelsRefresh{Status: "idle"}}, `{"catalog":{"revision":0,"availableModels":[]},"configuration":{"model":null,"thinkingLevel":"off"},"refresh":{"status":"idle"}}`},
		{"warning", ModelsRefresh{Status: "warning", Errors: map[string]string{"local": "unavailable"}}, `{"status":"warning","errors":{"local":"unavailable"}}`},
		{"warning empty", ModelsRefresh{Status: "warning", Errors: map[string]string{}}, `{"status":"warning","errors":{}}`},
		{"address", SessionAddress{ServerId: "local", SessionId: "session"}, `{"serverId":"local","sessionId":"session"}`},
		{"session", SessionSummary{SessionAddress: SessionAddress{ServerId: "local", SessionId: "session"}, CreatedAt: 1234}, `{"serverId":"local","sessionId":"session","createdAt":1234}`},
		{"directory", SessionDirectoryState{Sessions: []SessionSummary{}}, `{"revision":0,"sessions":[]}`},
		{"create omitted", SessionCreateOptions{}, `{}`},
		{"create empty", SessionCreateOptions{Id: new("")}, `{"id":""}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, err := json.Marshal(test.value)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != test.want {
				t.Fatalf("got %s, want %s", data, test.want)
			}
			decoded := reflect.New(reflect.TypeOf(test.value))
			if err := json.Unmarshal(data, decoded.Interface()); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded.Elem().Interface(), test.value) {
				t.Fatalf("round trip changed %#v", test.value)
			}
		})
	}
	if ModelsDefinition.Id() != "pi.models" || ModelsDefinition.Local() {
		t.Fatal("models token")
	}
	if SessionDirectoryDefinition.Id() != "pi.session-directory" || SessionDirectoryDefinition.Local() {
		t.Fatal("directory token")
	}
	if SessionManagementDefinition.Id() != "pi.session-management" || SessionManagementDefinition.Local() {
		t.Fatal("management token")
	}
}
