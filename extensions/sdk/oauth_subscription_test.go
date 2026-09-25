package sdk

import (
	"encoding/json"
	"testing"
)

func TestOAuthSubscriptionConfig(t *testing.T) {
	for _, subscription := range []bool{false, true} {
		provider := &OAuthProvider{IsSubscription: subscription}
		ext := New("subscription-test")
		ext.RegisterProvider("provider", ProviderConfig{"oauth": provider})
		var config struct {
			OAuth struct {
				IsSubscription bool `json:"isSubscription"`
			} `json:"oauth"`
		}
		if err := json.Unmarshal(ext.providers[0].Config, &config); err != nil {
			t.Fatal(err)
		}
		if config.OAuth.IsSubscription != subscription {
			t.Fatalf("subscription=%t: config=%s", subscription, ext.providers[0].Config)
		}
	}
}
