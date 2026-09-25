package signature

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/MichaelKinsy/PiG/internal/ownerfile"
)

// KeyID is the stable display and revocation identity of a public key.
func KeyID(public ed25519.PublicKey) string {
	sum := sha256.Sum256(public)
	return "ed25519:" + hex.EncodeToString(sum[:16])
}

// GenerateKey writes a new ed25519 key pair: the private key as PKCS#8 PEM at
// privatePath, readable by its owner only from the moment it exists, and the
// public key as PKIX PEM at privatePath+".pub" (mode 0644). It never replaces
// an existing file. If either file cannot be completed, it removes every path
// it created.
func GenerateKey(privatePath string) (string, error) {
	return generateKey(privatePath, openKeyFile, os.Remove)
}

// openKeyFile creates a new key file. A 0600 file is created owner-only by
// ownerfile.CreateNew, because the mode alone does not restrict access on
// Windows (D68).
func openKeyFile(path string, flag int, mode os.FileMode) (io.WriteCloser, error) {
	if mode == 0o600 {
		return ownerfile.CreateNew(path)
	}
	return os.OpenFile(path, flag, mode)
}

func generateKey(privatePath string, openFile func(string, int, os.FileMode) (io.WriteCloser, error), removeFile func(string) error) (string, error) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return "", err
	}
	publicPEM, err := MarshalPublicKey(public)
	if err != nil {
		return "", err
	}
	if err := writeNewWith(privatePath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0o600, openFile, removeFile); err != nil {
		return "", err
	}
	if err := writeNewWith(privatePath+".pub", publicPEM, 0o644, openFile, removeFile); err != nil {
		removeErr := removeFile(privatePath)
		if removeErr != nil {
			return "", errors.Join(err, fmt.Errorf("remove incomplete private key %s: %w", privatePath, removeErr))
		}
		return "", err
	}
	return KeyID(public), nil
}

func writeNewWith(path string, data []byte, mode os.FileMode, openFile func(string, int, os.FileMode) (io.WriteCloser, error), removeFile func(string) error) error {
	file, err := openFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	written, writeErr := file.Write(data)
	if writeErr != nil || written != len(data) {
		if writeErr == nil {
			writeErr = io.ErrShortWrite
		}
		closeErr := file.Close()
		removeErr := removeFile(path)
		return errors.Join(
			writeErr,
			wrapKeyCleanupError("close incomplete", path, closeErr),
			wrapKeyCleanupError("remove incomplete", path, removeErr),
		)
	}
	if closeErr := file.Close(); closeErr != nil {
		removeErr := removeFile(path)
		return errors.Join(closeErr, wrapKeyCleanupError("remove incomplete", path, removeErr))
	}
	return nil
}

func wrapKeyCleanupError(action, path string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s key file %s: %w", action, path, err)
}

// MarshalPublicKey encodes public as PKIX PEM.
func MarshalPublicKey(public ed25519.PublicKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
}

// ReadPrivateKey reads a PKCS#8 PEM ed25519 private key.
func ReadPrivateKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("%s is not a PEM PRIVATE KEY; create one with `pig piglet keygen`", path)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	private, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s is not an ed25519 private key", path)
	}
	return private, nil
}

// ParsePublicKeys decodes every PKIX PEM ed25519 public key in data. Any other
// content is an error.
func ParsePublicKeys(data []byte) ([]ed25519.PublicKey, error) {
	var keys []ed25519.PublicKey
	for rest := data; ; {
		block, remaining := pem.Decode(rest)
		if block == nil {
			if len(bytes.TrimSpace(rest)) > 0 {
				return nil, fmt.Errorf("public key data contains non-PEM content")
			}
			return keys, nil
		}
		if block.Type != "PUBLIC KEY" {
			return nil, fmt.Errorf("PEM block %q is not a PUBLIC KEY", block.Type)
		}
		key, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		public, ok := key.(ed25519.PublicKey)
		if !ok {
			return nil, fmt.Errorf("public key is not ed25519")
		}
		keys = append(keys, public)
		rest = remaining
	}
}
