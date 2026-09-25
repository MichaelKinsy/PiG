// Package release verifies and installs signed Piglet Binary releases.
//
// pig additive (D18): Piglet releases are product-neutral signed indexes whose
// per-target binaries can be pulled without contacting a Pi or PiG service.
package release

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
)

// PayloadType is the DSSE payload type of a Piglet release index.
const PayloadType = "application/vnd.pig.piglet-release+json"

// Index names one Piglet release and its available target binaries.
type Index struct {
	Piglet     string            `json:"piglet"`
	Version    string            `json:"version"`
	PigVersion string            `json:"pigVersion"`
	SourceRef  string            `json:"sourceRef"`
	Signer     signature.Signer  `json:"signer"`
	Binaries   map[string]Binary `json:"binaries"`
}

// Binary is one downloadable signed Piglet Binary. SHA256 covers the complete
// downloaded file, including its signature trailer.
type Binary struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// VerifiedIndex is an index whose embedded public key verified its DSSE
// envelope. The key is still subject to the caller's trust and continuity
// policy.
type VerifiedIndex struct {
	Index     Index
	Envelope  signature.Envelope
	PublicKey ed25519.PublicKey
}

// Sign signs index with key and returns its DSSE envelope as JSON.
func Sign(index Index, key ed25519.PrivateKey) ([]byte, error) {
	public, ok := key.Public().(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("Piglet release signing key is not ed25519")
	}
	index.Signer = signature.Signer{
		KeyID:     signature.KeyID(public),
		PublicKey: base64.StdEncoding.EncodeToString(public),
	}
	if err := Validate(index); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(index)
	if err != nil {
		return nil, err
	}
	envelope := signature.SignEnvelope(PayloadType, payload, key)
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// Verify strictly decodes a release envelope, validates its index, and checks
// the DSSE signature with the public key named by the signed index.
func Verify(data []byte) (VerifiedIndex, error) {
	envelope, err := signature.ParseEnvelope(data)
	if err != nil {
		return VerifiedIndex{}, fmt.Errorf("Piglet release index: %w", err)
	}
	payload, err := base64.StdEncoding.DecodeString(envelope.Payload)
	if err != nil {
		return VerifiedIndex{}, fmt.Errorf("Piglet release index payload: %w", err)
	}
	var index Index
	if err := strictJSON(payload, &index); err != nil {
		return VerifiedIndex{}, fmt.Errorf("Piglet release index payload: %w", err)
	}
	if err := Validate(index); err != nil {
		return VerifiedIndex{}, err
	}
	public, err := base64.StdEncoding.DecodeString(index.Signer.PublicKey)
	if err != nil || len(public) != ed25519.PublicKeySize || index.Signer.KeyID != signature.KeyID(public) {
		return VerifiedIndex{}, fmt.Errorf("Piglet release index names an invalid ed25519 public key")
	}
	if _, err := envelope.Verify(PayloadType, ed25519.PublicKey(public)); err != nil {
		return VerifiedIndex{}, fmt.Errorf("Piglet release index: %w", err)
	}
	return VerifiedIndex{Index: index, Envelope: envelope, PublicKey: ed25519.PublicKey(public)}, nil
}

// Validate checks the strict current shape of a Piglet release index.
func Validate(index Index) error {
	if !validPathPart(index.Piglet) {
		return fmt.Errorf("Piglet release index has invalid Piglet name %q", index.Piglet)
	}
	if !validReleaseVersion(index.Version) {
		return fmt.Errorf("Piglet release index has invalid version %q", index.Version)
	}
	if index.PigVersion == "" || index.PigVersion != strings.TrimSpace(index.PigVersion) {
		return fmt.Errorf("Piglet release index has invalid Pig version %q", index.PigVersion)
	}
	if index.SourceRef == "" || index.SourceRef != strings.TrimSpace(index.SourceRef) {
		return fmt.Errorf("Piglet release index has invalid sourceRef %q", index.SourceRef)
	}
	if index.Signer.KeyID == "" || index.Signer.PublicKey == "" {
		return fmt.Errorf("Piglet release index signer is required")
	}
	if len(index.Binaries) == 0 {
		return fmt.Errorf("Piglet release index has no binaries")
	}
	for target, binary := range index.Binaries {
		if !validTarget(target) {
			return fmt.Errorf("Piglet release index has invalid target %q", target)
		}
		parsed, err := url.Parse(binary.URL)
		if err != nil || binary.URL == "" || parsed.Fragment != "" {
			return fmt.Errorf("Piglet release target %s has invalid URL", target)
		}
		if len(binary.SHA256) != 64 {
			return fmt.Errorf("Piglet release target %s has invalid SHA256 %q", target, binary.SHA256)
		}
		decoded, err := hex.DecodeString(binary.SHA256)
		if err != nil || hex.EncodeToString(decoded) != binary.SHA256 {
			return fmt.Errorf("Piglet release target %s has invalid SHA256 %q", target, binary.SHA256)
		}
		if binary.Size <= 0 {
			return fmt.Errorf("Piglet release target %s has invalid size %d", target, binary.Size)
		}
	}
	return nil
}

func validReleaseVersion(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && semver.IsValid("v"+value)
}

func validTarget(target string) bool {
	goos, goarch, ok := strings.Cut(target, "/")
	return ok && validPathPart(goos) && validPathPart(goarch) && !strings.Contains(goarch, "/")
}

func validPathPart(value string) bool {
	return value != "" && value != "." && value != ".." && value == strings.TrimSpace(value) && !strings.ContainsAny(value, `/\\`)
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
