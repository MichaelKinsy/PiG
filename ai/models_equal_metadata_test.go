package ai

import "testing"

func TestModelsAreEqualUsesDeclaredProviderAndID(t *testing.T) {
	// Pi models.ts:960-965 compares ID/provider strings; live transport construction and API kind are irrelevant.
	metadata := func(id, provider string) *Model {
		return &Model{ID: id, ProviderMeta: ProviderMetadata{ProviderID: provider}}
	}
	for _, tc := range []struct {
		name string
		a, b *Model
		want bool
	}{
		{"metadata only", metadata("shared", "one"), metadata("shared", "one"), true},
		{"metadata and native backend", metadata("shared", "one"), &Model{ID: "shared", Provider: stubProvider{id: "one"}}, true},
		{"different declared providers", metadata("shared", "one"), metadata("shared", "two"), false},
		{"different IDs", metadata("first", "one"), metadata("second", "one"), false},
		{"backend identity does not override declared provider", &Model{ID: "shared", Provider: stubProvider{id: "transport-a"}, ProviderMeta: ProviderMetadata{ProviderID: "one"}}, &Model{ID: "shared", Provider: stubProvider{id: "transport-b"}, ProviderMeta: ProviderMetadata{ProviderID: "one"}}, true},
		{"API kind is not model identity", &Model{ID: "shared", ProviderMeta: ProviderMetadata{ProviderID: "one", API: APIOpenAICompletions}}, &Model{ID: "shared", ProviderMeta: ProviderMetadata{ProviderID: "one", API: APIOpenAIResponses}}, true},
		{"empty non-nil models", &Model{}, &Model{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ModelsAreEqual(tc.a, tc.b); got != tc.want {
				t.Fatalf("ModelsAreEqual=%t want=%t", got, tc.want)
			}
			if got := ModelsAreEqual(tc.b, tc.a); got != tc.want {
				t.Fatalf("reversed ModelsAreEqual=%t want=%t", got, tc.want)
			}
		})
	}
}
