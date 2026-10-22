package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// Ports system-one-shared.ts SystemOneRequest.images?: string[]: an absent member and an explicit [] are different wire bodies.
func TestSystemOneWireRequestImagesKeepAnExplicitEmptyList(t *testing.T) {
	absent := SystemOneWireRequest{State: "s"}.Body()
	if _, ok := absent["images"]; ok {
		t.Fatalf("a request without images carries an images member: %v", absent)
	}
	empty := SystemOneWireRequest{State: "s", Images: []string{}}
	body := empty.Body()
	if images, ok := body["images"].([]string); !ok || images == nil || len(images) != 0 {
		t.Fatalf("an explicit empty images list = %#v, want present and empty", body["images"])
	}
	encoded, err := json.Marshal(body["images"])
	if err != nil || string(encoded) != "[]" {
		t.Fatalf("images encodes as %s (%v), want []", encoded, err)
	}
	direct, err := json.Marshal(empty)
	if err != nil || !strings.Contains(string(direct), `"Images":[]`) {
		t.Fatalf("the request struct encodes as %s (%v), want an explicit empty Images", direct, err)
	}
}

// Ports typesafe-system-one.ts:19-22 detectCompat: OpenRouter by provider ID (surviving a baseUrl override) or by host (regional
// endpoints), TypeSafe's assets form with the opt-in header otherwise.
func TestTypesafeSystemOneDetectCompatShapes(t *testing.T) {
	openRouter := ResolvedSystemOneCompat{ImageInput: SystemOneImageInputStateImageURLs}
	typesafe := ResolvedSystemOneCompat{ImageInput: SystemOneImageInputAssets, SendImageOptInHeader: true}
	for _, tc := range []struct {
		name  string
		model ClassifierModel
		want  ResolvedSystemOneCompat
	}{
		{"provider id behind a proxy", ClassifierModel{Provider: "openrouter", BaseURL: "https://proxy.example/v1"}, openRouter},
		{"host of a regional endpoint", ClassifierModel{Provider: "custom", BaseURL: "https://us.openrouter.ai/api/v1"}, openRouter},
		{"typesafe", ClassifierModel{Provider: "typesafe", BaseURL: "https://api.typesafe.ai"}, typesafe},
		{"unknown provider and host", ClassifierModel{Provider: "custom", BaseURL: "https://proxy.example/v1"}, typesafe},
	} {
		if got := typesafeSystemOneTransport.DetectCompat(tc.model); got != tc.want {
			t.Errorf("%s: DetectCompat = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}
