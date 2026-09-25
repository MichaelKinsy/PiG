package signature

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

const testPayloadType = "application/vnd.pig.test+json"

func TestEnvelopeRoundTripAndRejections(t *testing.T) {
	key, other := newKey(t), newKey(t)
	envelope := SignEnvelope(testPayloadType, []byte(`{"release":"1.0.0"}`), key)
	data, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseEnvelope(data)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := parsed.SignerKeyID(); err != nil || id != KeyID(publicOf(key)) {
		t.Fatalf("SignerKeyID() = %q, %v", id, err)
	}
	if payload, err := parsed.Verify(testPayloadType, publicOf(key)); err != nil || string(payload) != `{"release":"1.0.0"}` {
		t.Fatalf("Verify() = %q, %v", payload, err)
	}

	tamperedPayload := parsed
	tamperedPayload.Payload = base64.StdEncoding.EncodeToString([]byte(`{"release":"9.9.9"}`))
	twoSignatures := parsed
	twoSignatures.Signatures = append(twoSignatures.Signatures, parsed.Signatures[0])
	for name, check := range map[string]struct {
		envelope    Envelope
		payloadType string
		key         []byte
		want        string
	}{
		"wrong key":       {parsed, testPayloadType, publicOf(other), "does not match the verifying key"},
		"wrong type":      {parsed, "application/other", publicOf(key), "payload type"},
		"altered payload": {tamperedPayload, testPayloadType, publicOf(key), "does not verify"},
		"two signatures":  {twoSignatures, testPayloadType, publicOf(key), "exactly one signature"},
	} {
		if _, err := check.envelope.Verify(check.payloadType, check.key); err == nil || !strings.Contains(err.Error(), check.want) {
			t.Errorf("%s: Verify() error = %v, want %q", name, err, check.want)
		}
	}
	if _, err := ParseEnvelope(append(data[:len(data)-1], []byte(`,"extra":1}`)...)); err == nil {
		t.Fatal("envelope with an unknown field was accepted")
	}
}

func TestParseEnvelopeRejectsTrailingJSONContent(t *testing.T) {
	envelope := SignEnvelope(testPayloadType, []byte(`{"release":"1.0.0"}`), newKey(t))
	data, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"}", "]", "{}", "null", "garbage"} {
		t.Run(suffix, func(t *testing.T) {
			if _, err := ParseEnvelope(append(append([]byte(nil), data...), suffix...)); err == nil {
				t.Fatalf("ParseEnvelope accepted trailing content %q", suffix)
			}
		})
	}
	if _, err := ParseEnvelope(append(data, '\n', '\t', ' ')); err != nil {
		t.Fatalf("ParseEnvelope rejected trailing JSON whitespace: %v", err)
	}
}
