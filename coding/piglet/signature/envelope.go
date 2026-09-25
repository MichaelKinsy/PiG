package signature

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
)

// Envelope is a DSSE envelope carrying exactly one ed25519 signature. It is
// the signed container for every Piglet signature: the Piglet Binary block
// and any other Pig document that needs offline authenticity.
type Envelope struct {
	PayloadType string              `json:"payloadType"`
	Payload     string              `json:"payload"`
	Signatures  []EnvelopeSignature `json:"signatures"`
}

// EnvelopeSignature is one DSSE signature and the key ID that made it.
type EnvelopeSignature struct {
	KeyID string `json:"keyid"`
	Sig   string `json:"sig"`
}

// PAE is the DSSE v1 pre-authentication encoding the signature covers.
func PAE(payloadType string, payload []byte) []byte {
	return fmt.Appendf(nil, "DSSEv1 %d %s %d %s", len(payloadType), payloadType, len(payload), payload)
}

// SignEnvelope signs payload as payloadType with key.
func SignEnvelope(payloadType string, payload []byte, key ed25519.PrivateKey) Envelope {
	public := key.Public().(ed25519.PublicKey)
	return Envelope{
		PayloadType: payloadType, Payload: base64.StdEncoding.EncodeToString(payload),
		Signatures: []EnvelopeSignature{{KeyID: KeyID(public), Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(key, PAE(payloadType, payload)))}},
	}
}

// ParseEnvelope strictly decodes exactly one DSSE envelope, allowing only JSON whitespace afterward. It does not verify the signature.
func ParseEnvelope(data []byte) (Envelope, error) {
	var envelope Envelope
	if err := strictJSON(data, &envelope); err != nil {
		return Envelope{}, fmt.Errorf("signature envelope: %w", err)
	}
	return envelope, nil
}

// SignerKeyID is the key ID the envelope's single signature names, for
// looking up the verifying key. It is a claim until Verify succeeds.
func (e Envelope) SignerKeyID() (string, error) {
	if len(e.Signatures) != 1 {
		return "", fmt.Errorf("signature envelope must carry exactly one signature, found %d", len(e.Signatures))
	}
	return e.Signatures[0].KeyID, nil
}

// Verify checks that e is a payloadType envelope whose single signature was
// made by public over the payload, and returns the payload.
func (e Envelope) Verify(payloadType string, public ed25519.PublicKey) ([]byte, error) {
	if e.PayloadType != payloadType {
		return nil, fmt.Errorf("signature envelope has payload type %q, want %q", e.PayloadType, payloadType)
	}
	keyID, err := e.SignerKeyID()
	if err != nil {
		return nil, err
	}
	if len(public) != ed25519.PublicKeySize || keyID != KeyID(public) {
		return nil, fmt.Errorf("signature key ID %s does not match the verifying key", keyID)
	}
	payload, err := base64.StdEncoding.DecodeString(e.Payload)
	if err != nil {
		return nil, fmt.Errorf("signature payload: %w", err)
	}
	sig, err := base64.StdEncoding.DecodeString(e.Signatures[0].Sig)
	if err != nil || !ed25519.Verify(public, PAE(payloadType, payload), sig) {
		return nil, fmt.Errorf("signature by %s does not verify: the payload or signature was altered", keyID)
	}
	return payload, nil
}

func strictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("trailing content")
	}
	return nil
}
