package release

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
)

func TestReleaseIndexSignVerifyRoundTrip(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	index := Index{
		Piglet: "porter", Version: "1.2.3", PigVersion: "pig-test", SourceRef: "npm:@example/porter@1.2.3",
		Binaries: map[string]Binary{"linux/amd64": {URL: "https://example.invalid/pig-porter", SHA256: strings.Repeat("a", 64), Size: 42}},
	}
	data, err := Sign(index, key)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := Verify(data)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Index.Piglet != index.Piglet || verified.Index.Signer.KeyID != signature.KeyID(key.Public().(ed25519.PublicKey)) {
		t.Fatalf("verified = %#v", verified.Index)
	}
	for _, suffix := range []string{"}", "]", "{}", "garbage"} {
		t.Run("trailing "+suffix, func(t *testing.T) {
			if _, err := Verify(append(append([]byte(nil), data...), suffix...)); err == nil {
				t.Fatalf("Verify accepted a release envelope with trailing content %q", suffix)
			}
		})
	}
}

func TestReleaseIndexRejectsUnknownPayloadFieldsAndTampering(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	data, err := Sign(Index{
		Piglet: "porter", Version: "1.2.3", PigVersion: "pig-test", SourceRef: "npm:porter@1.2.3",
		Binaries: map[string]Binary{"linux/amd64": {URL: "https://example.invalid/pig", SHA256: strings.Repeat("b", 64), Size: 1}},
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	var envelope signature.Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	payload, err := base64.StdEncoding.DecodeString(envelope.Payload)
	if err != nil {
		t.Fatal(err)
	}
	payload = []byte(strings.Replace(string(payload), `"piglet":"porter"`, `"piglet":"porter","unknown":true`, 1))
	envelope.Payload = base64.StdEncoding.EncodeToString(payload)
	data, err = json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(data); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Verify() error = %v", err)
	}
}
