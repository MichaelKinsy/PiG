// Package signature signs and verifies Piglet Binaries.
//
// pig additive (D18): a signed Piglet Binary carries one signature block
// appended after its executable bytes:
//
//	executable bytes | DSSE envelope (JSON) | 8-byte big-endian envelope length | "PIGLET-SIGNATURE"
//
// The envelope's payload is a Manifest that names the Piglet, target, Pig
// version, Piglet definition digest, resolution record, every component
// digest, and the SHA-256 and size of the executable bytes before the block.
// The signature is ed25519 over the DSSE pre-authentication encoding of the
// payload, so changing any executable byte, the payload, or the signature
// fails verification. Verification needs no network.
package signature

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// PayloadType is the DSSE payload type of a Piglet Binary signature manifest.
const PayloadType = "application/vnd.pig.piglet-binary-manifest+json"

const (
	trailerMagic    = "PIGLET-SIGNATURE"
	footerSize      = 8 + len(trailerMagic)
	maxEnvelopeSize = 1 << 20
)

// Component is one executable/runtime component named by the Piglet plan.
type Component struct {
	Kind            string `json:"kind"`
	Name            string `json:"name"`
	Realization     string `json:"realization"`
	Materialization string `json:"materialization"`
	Digest          string `json:"digest"`
}

// EmbeddedFile is one payload compiled into the executable, such as a
// prebuilt extension cell.
type EmbeddedFile struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

// Executable identifies the executable bytes before the signature block.
type Executable struct {
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

// Signer names the key that signed the manifest.
type Signer struct {
	KeyID     string `json:"keyId"`
	PublicKey string `json:"publicKey"`
}

// Manifest is the signed statement about one Piglet Binary.
type Manifest struct {
	Piglet              string         `json:"piglet"`
	ReleaseVersion      string         `json:"releaseVersion,omitempty"`
	Target              string         `json:"target"`
	PigVersion          string         `json:"pigVersion"`
	PigletDigest        string         `json:"pigletDigest"`
	SourceDigest        string         `json:"sourceDigest"`
	ResolutionDigest    string         `json:"resolutionDigest"`
	ComponentPlanDigest string         `json:"componentPlanDigest"`
	Components          []Component    `json:"components"`
	Embedded            []EmbeddedFile `json:"embedded"`
	Executable          Executable     `json:"executable"`
	Signer              Signer         `json:"signer"`
}

// Sign appends a signature block for manifest to the unsigned executable at
// path. It fills the manifest's Executable and Signer fields.
func Sign(path string, manifest Manifest, key ed25519.PrivateKey) (Manifest, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return Manifest{}, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return Manifest{}, err
	}
	if _, found, err := readBlock(file, info.Size()); found {
		return Manifest{}, fmt.Errorf("%s already carries a Piglet signature block", path)
	} else if err != nil {
		return Manifest{}, err
	}
	digest, err := digestPrefix(file, info.Size())
	if err != nil {
		return Manifest{}, err
	}
	public, ok := key.Public().(ed25519.PublicKey)
	if !ok {
		return Manifest{}, fmt.Errorf("signing key is not ed25519")
	}
	manifest.Executable = Executable{Digest: digest, Size: info.Size()}
	manifest.Signer = Signer{KeyID: KeyID(public), PublicKey: base64.StdEncoding.EncodeToString(public)}
	payload, err := json.Marshal(manifest)
	if err != nil {
		return Manifest{}, err
	}
	block, err := json.Marshal(SignEnvelope(PayloadType, payload, key))
	if err != nil {
		return Manifest{}, err
	}
	block = binary.BigEndian.AppendUint64(block, uint64(len(block)))
	block = append(block, trailerMagic...)
	if _, err := file.WriteAt(block, info.Size()); err != nil {
		return Manifest{}, fmt.Errorf("append Piglet signature block: %w", err)
	}
	return manifest, file.Close()
}

// signedBlock is a parsed, not yet verified, signature block.
type signedBlock struct {
	envelope Envelope
	manifest Manifest
	execSize int64
}

// readBlock returns the signature block at the end of file, found=false when
// the file carries none. A block that is present but malformed is an error.
func readBlock(file io.ReaderAt, size int64) (signedBlock, bool, error) {
	if size < int64(footerSize) {
		return signedBlock{}, false, nil
	}
	footer := make([]byte, footerSize)
	if _, err := file.ReadAt(footer, size-int64(footerSize)); err != nil {
		return signedBlock{}, false, err
	}
	if string(footer[8:]) != trailerMagic {
		return signedBlock{}, false, nil
	}
	length := binary.BigEndian.Uint64(footer[:8])
	if length == 0 || length > maxEnvelopeSize || int64(length) > size-int64(footerSize) {
		return signedBlock{}, true, fmt.Errorf("Piglet signature block has an invalid length")
	}
	block := signedBlock{execSize: size - int64(footerSize) - int64(length)}
	raw := make([]byte, length)
	if _, err := file.ReadAt(raw, block.execSize); err != nil {
		return signedBlock{}, true, err
	}
	envelope, err := ParseEnvelope(raw)
	if err != nil {
		return signedBlock{}, true, fmt.Errorf("Piglet %w", err)
	}
	block.envelope = envelope
	payload, err := base64.StdEncoding.DecodeString(envelope.Payload)
	if err != nil {
		return signedBlock{}, true, fmt.Errorf("Piglet signature payload: %w", err)
	}
	if err := strictJSON(payload, &block.manifest); err != nil {
		return signedBlock{}, true, fmt.Errorf("Piglet signature manifest: %w", err)
	}
	return block, true, nil
}

// digestPrefix returns the SHA-256 of the first size bytes of file.
func digestPrefix(file io.ReaderAt, size int64) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, io.NewSectionReader(file, 0, size)); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// verifyBlock checks the envelope signature with the key the manifest names
// and binds the manifest to the executable bytes before the block.
func verifyBlock(file io.ReaderAt, block signedBlock) (ed25519.PublicKey, error) {
	public, err := base64.StdEncoding.DecodeString(block.manifest.Signer.PublicKey)
	if err != nil || len(public) != ed25519.PublicKeySize || block.manifest.Signer.KeyID != KeyID(public) {
		return nil, fmt.Errorf("Piglet signature names an invalid ed25519 public key")
	}
	if _, err := block.envelope.Verify(PayloadType, public); err != nil {
		return nil, fmt.Errorf("Piglet %w", err)
	}
	if block.manifest.Executable.Size != block.execSize {
		return nil, fmt.Errorf("Piglet Binary is %d bytes before its signature block, signed manifest says %d", block.execSize, block.manifest.Executable.Size)
	}
	digest, err := digestPrefix(file, block.execSize)
	if err != nil {
		return nil, err
	}
	if digest != block.manifest.Executable.Digest {
		return nil, fmt.Errorf("Piglet Binary executable bytes changed after signing: digest %s, signed %s", digest, block.manifest.Executable.Digest)
	}
	return public, nil
}

// Status is the outcome of checking one Piglet Binary's signature.
type Status struct {
	Signed   bool
	Manifest Manifest
	KeyID    string
	// Trusted reports that the signing key is in the user's trust store.
	Trusted bool
	// Embedded reports that the signing key is one the binary's author embedded.
	Embedded bool
}

// Describe is the one-line user-facing signature state. It never claims more
// than the check established.
func (s Status) Describe() string {
	switch {
	case !s.Signed:
		return "unsigned (no signature: anyone who can rewrite this file can rewrite its build record)"
	case s.Trusted:
		return "signed by " + s.KeyID + " (a key in your Piglet trust store)"
	case s.Embedded:
		return "signed by " + s.KeyID + " (the key its author embedded; not in your Piglet trust store, so this proves it is unchanged since that key signed it, not who holds the key)"
	default:
		return "signed by " + s.KeyID + " (not in your Piglet trust store: this proves it is unchanged since that key signed it, not who holds the key)"
	}
}

// Policy is the trust applied to one signature check.
type Policy struct {
	// Trust is the user's trust store.
	Trust Trust
	// Embedded are the public keys the binary's author compiled into it.
	// When set, the binary must be signed by one of them or a trusted key.
	Embedded []ed25519.PublicKey
	// RequireKnownSigner rejects a signature by a key that is neither
	// embedded nor trusted. The binary's own startup check sets it.
	RequireKnownSigner bool
}

// Check reads and verifies the signature block of the file at path under
// policy. The returned Status describes what was found even when err is set.
func Check(path string, policy Policy) (Status, error) {
	file, err := os.Open(path)
	if err != nil {
		return Status{}, err
	}
	defer func() { _ = file.Close() }() // Read-only: close cannot lose data.
	return CheckFile(file, policy)
}

// CheckFile verifies an already opened Binary without reopening its path. The caller owns the file; verification does not change its offset.
func CheckFile(file *os.File, policy Policy) (Status, error) {
	info, err := file.Stat()
	if err != nil {
		return Status{}, err
	}
	block, found, err := readBlock(file, info.Size())
	if err != nil {
		return Status{Signed: found}, err
	}
	if !found {
		return Status{}, unsignedError(policy)
	}
	status := Status{Signed: true, Manifest: block.manifest, KeyID: block.manifest.Signer.KeyID}
	public, err := verifyBlock(file, block)
	if err != nil {
		return status, err
	}
	_, status.Trusted = policy.Trust.Keys[status.KeyID]
	for _, key := range policy.Embedded {
		status.Embedded = status.Embedded || key.Equal(public)
	}
	return status, signerError(status, policy)
}

func unsignedError(policy Policy) error {
	switch {
	case len(policy.Embedded) > 0:
		return fmt.Errorf("Piglet Binary signature is missing: its author embedded signer %s, but the file carries no signature block", keyList(policy.Embedded))
	case policy.Trust.RequireSignature:
		return fmt.Errorf("Piglet Binary is unsigned and your Piglet trust policy requires a signature (%s)", policy.Trust.requirePath())
	}
	return nil
}

func signerError(status Status, policy Policy) error {
	switch {
	case policy.Trust.Revoked[status.KeyID]:
		return fmt.Errorf("Piglet Binary is signed by %s, a key revoked in your Piglet trust store", status.KeyID)
	case policy.Trust.RequireSignature && !status.Trusted:
		return fmt.Errorf("Piglet Binary is signed by %s, which is not in your Piglet trust store, and your policy requires a trusted signature (%s)", status.KeyID, policy.Trust.requirePath())
	case policy.RequireKnownSigner && !status.Trusted && !status.Embedded:
		return fmt.Errorf("Piglet Binary is signed by %s, which is neither its author's embedded key nor in your Piglet trust store", status.KeyID)
	}
	return nil
}

func keyList(keys []ed25519.PublicKey) string {
	ids := make([]string, len(keys))
	for i, key := range keys {
		ids[i] = KeyID(key)
	}
	return strings.Join(ids, ", ")
}
