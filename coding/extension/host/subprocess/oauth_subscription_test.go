package subprocess

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func BenchmarkOAuthSubscriptionMetadata(b *testing.B) {
	const name = "subscription-benchmark"
	host := &Host{}
	me := &managedExt{}
	b.Cleanup(func() { host.unregisterOAuthProviders(me, nil) })
	if err := host.registerOAuthProvider(me, name, json.RawMessage(`{"oauth":{"isSubscription":true}}`)); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if !ai.IsOAuthSubscriptionProvider(name) {
			b.Fatal("subscription metadata missing")
		}
	}
}

// Pi's provider-composer.ts retains config.oauth.isSubscription, and
// ModelRuntime.isUsingSubscription tests it with === true, not OAuth presence.
func TestOAuthSubscriptionRegistration(t *testing.T) {
	for _, store := range []bool{false, true} {
		for _, field := range []string{"", `,"isSubscription":false`, `,"isSubscription":true`, `,"isSubscription":null`} {
			config := `{"oauth":{"name":"Subscription test","has_login":true,"has_credential_store":` + map[bool]string{false: "false", true: "true"}[store] + field + `}}`
			t.Run(config, func(t *testing.T) {
				const name = "subscription-registration-test"
				host := &Host{}
				me := &managedExt{}
				t.Cleanup(func() { host.unregisterOAuthProviders(me, nil) })
				if err := host.registerOAuthProvider(me, name, json.RawMessage(config)); err != nil {
					t.Fatal(err)
				}
				want := field == `,"isSubscription":true`
				if got := ai.IsOAuthSubscriptionProvider(name); got != want {
					t.Fatalf("subscription = %t, want %t", got, want)
				}
				// A replacement registration must not retain the old true value.
				if err := host.registerOAuthProvider(me, name, json.RawMessage(`{"oauth":{"name":"replacement"}}`)); err != nil {
					t.Fatal(err)
				}
				if ai.IsOAuthSubscriptionProvider(name) {
					t.Fatal("replacement retained subscription metadata")
				}
				host.unregisterOAuthProviders(me, nil)
				if _, exists := ai.GetOAuthProvider(name); exists {
					t.Fatal("removed extension retained OAuth provider")
				}
			})
		}
	}
}
