package extension

import (
	"encoding/json"
	"testing"
)

func TestProviderOAuthSubscriptionJSON(t *testing.T) {
	for _, raw := range []string{`{"oauth":{}}`, `{"oauth":{"isSubscription":false}}`, `{"oauth":{"isSubscription":true}}`} {
		var config ProviderConfig
		if err := json.Unmarshal([]byte(raw), &config); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			OAuth struct {
				IsSubscription bool `json:"isSubscription"`
			} `json:"oauth"`
		}
		if err := json.Unmarshal(encoded, &got); err != nil {
			t.Fatal(err)
		}
		if want := raw == `{"oauth":{"isSubscription":true}}`; got.OAuth.IsSubscription != want {
			t.Fatalf("round trip %s = %s, want subscription=%t", raw, encoded, want)
		}
	}
}
