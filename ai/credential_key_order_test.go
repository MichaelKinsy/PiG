package ai

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Pi keeps the objects JSON.parse read from auth.json and writes them back with JSON.stringify(data, null, 2) (auth-storage.ts:465-467), so every credential keeps its property order, its env order and JavaScript's integer-key-first order. The expected text is node's JSON.stringify output for the same input.
func TestAuthStorageWritesCredentialsInTheirDecodedKeyOrder(t *testing.T) {
	store, path := authReloadFile(t, `{"p":{"key":"k","type":"api_key","env":{"Z":"1","A":"2"},"2":"x","1":"y"},"o":{"refresh":"r","type":"oauth","access":"a","expires":1,"zeta":"x","alpha":"y"}}`)
	if _, err := store.Modify(t.Context(), "n", func(*Credential) (*Credential, error) {
		return &Credential{Type: CredentialAPIKey, Key: "n"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	want := `{
  "p": {
    "1": "y",
    "2": "x",
    "key": "k",
    "type": "api_key",
    "env": {
      "Z": "1",
      "A": "2"
    }
  },
  "o": {
    "refresh": "r",
    "type": "oauth",
    "access": "a",
    "expires": 1,
    "zeta": "x",
    "alpha": "y"
  },
  "n": {
    "type": "api_key",
    "key": "n"
  }
}`
	if got, err := os.ReadFile(path); err != nil || string(got) != want {
		t.Fatalf("auth.json=\n%s\n%v\nwant\n%s", got, err, want)
	}
}

// A login result is stored as the object the flow returned: Pi's modify stores next as given (auth-storage.ts:464-467). A changed property keeps its place, as { ...credential, key } does.
func TestAuthStorageStoresALoginCredentialInItsOwnKeyOrder(t *testing.T) {
	var credential Credential
	if err := json.Unmarshal([]byte(`{"env":{"WIRING_GATEWAY_URL":"http://x"},"key":"k","type":"api_key"}`), &credential); err != nil {
		t.Fatal(err)
	}
	store, path := authReloadFile(t, "")
	if _, err := store.Modify(t.Context(), "p", func(*Credential) (*Credential, error) { return &credential, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Modify(t.Context(), "p", func(current *Credential) (*Credential, error) {
		current.Key = "rotated"
		return current, nil
	}); err != nil {
		t.Fatal(err)
	}
	want := `{
  "p": {
    "env": {
      "WIRING_GATEWAY_URL": "http://x"
    },
    "key": "rotated",
    "type": "api_key"
  }
}`
	if got, err := os.ReadFile(path); err != nil || string(got) != want {
		t.Fatalf("auth.json=\n%s\n%v\nwant\n%s", got, err, want)
	}
}

// Pi's adaptOAuth stores { ...credential, type: "oauth" } (provider-composer.ts:367): a flow's own discriminator keeps its place and an added one follows the flow's properties.
func TestCredentialFromOAuthPlacesTheDiscriminatorAsPiDoes(t *testing.T) {
	for _, tc := range []struct{ name, flow, want string }{
		{"added", `{"refresh":"r","access":"a","expires":5,"zeta":1}`, `{"refresh":"r","access":"a","expires":5,"zeta":1,"type":"oauth"}`},
		{"own", `{"type":"oauth","refresh":"r","access":"a","expires":5}`, `{"type":"oauth","refresh":"r","access":"a","expires":5}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var flow OAuthCredentials
			if err := json.Unmarshal([]byte(tc.flow), &flow); err != nil {
				t.Fatal(err)
			}
			credential, err := credentialFromOAuth(flow)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := json.Marshal(credential); err != nil || string(got) != tc.want {
				t.Fatalf("stored=%s, %v want %s", got, err, tc.want)
			}
		})
	}
}

// A credential built in Go, or decoded in the codec's own order, carries no order, so its value equals the same credential built field by field.
func TestCredentialInCodecOrderCarriesNoKeyOrder(t *testing.T) {
	for _, raw := range []string{`{"type":"api_key","key":"k"}`, `{"access":"a","expires":1,"refresh":"r","type":"oauth"}`} {
		var credential Credential
		if err := json.Unmarshal([]byte(raw), &credential); err != nil {
			t.Fatal(err)
		}
		if credential.order.keys != nil || credential.order.env != nil {
			t.Fatalf("%s: order=%#v", raw, credential.order)
		}
	}
}
