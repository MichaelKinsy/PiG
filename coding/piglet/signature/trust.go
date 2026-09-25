package signature

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Trust is the user's Piglet signing trust store:
//
//	<dir>/keys/*.pub          trusted ed25519 public keys (PKIX PEM)
//	<dir>/revoked             revoked key IDs, one per line ('#' comments)
//	<dir>/require-signature   present: every Piglet Binary must carry a
//	                          signature by a trusted key
//
// A revoked key is never trusted, even when its .pub file remains.
type Trust struct {
	Dir              string
	Keys             map[string]ed25519.PublicKey
	Revoked          map[string]bool
	RequireSignature bool
}

// TrustDir is the user's Piglet trust store directory.
func TrustDir() string {
	return filepath.Join(codingagent.ConfigRoot(), "piglet-trust")
}

func (t Trust) requirePath() string {
	return filepath.Join(t.Dir, "require-signature")
}

// LoadTrust reads the trust store in dir. A missing store is empty. Any
// malformed file is an error so a damaged policy never silently widens trust.
func LoadTrust(dir string) (Trust, error) {
	trust := Trust{Dir: dir, Keys: map[string]ed25519.PublicKey{}, Revoked: map[string]bool{}}
	if err := trust.loadRevoked(); err != nil {
		return Trust{}, err
	}
	paths, err := filepath.Glob(filepath.Join(dir, "keys", "*.pub"))
	if err != nil {
		return Trust{}, err
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return Trust{}, err
		}
		keys, err := ParsePublicKeys(data)
		if err != nil {
			return Trust{}, fmt.Errorf("Piglet trust key %s: %w", path, err)
		}
		for _, key := range keys {
			if id := KeyID(key); !trust.Revoked[id] {
				trust.Keys[id] = key
			}
		}
	}
	switch _, err := os.Stat(trust.requirePath()); {
	case err == nil:
		trust.RequireSignature = true
	case !errors.Is(err, os.ErrNotExist):
		return Trust{}, err
	}
	return trust, nil
}

func (t *Trust) loadRevoked() error {
	data, err := os.ReadFile(filepath.Join(t.Dir, "revoked"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		if !validKeyID(text) {
			return fmt.Errorf("%s:%d: %q is not a Piglet key ID", filepath.Join(t.Dir, "revoked"), line, text)
		}
		t.Revoked[text] = true
	}
	return scanner.Err()
}

func validKeyID(id string) bool {
	hexPart, ok := strings.CutPrefix(id, "ed25519:")
	return ok && len(hexPart) == 32 && strings.Trim(hexPart, "0123456789abcdef") == ""
}

// TrustKeys copies the public keys in pemData into the trust store and returns
// their key IDs. A revoked key cannot be trusted again.
func TrustKeys(dir string, pemData []byte) ([]string, error) {
	keys, err := ParsePublicKeys(pemData)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no PEM PUBLIC KEY found")
	}
	trust, err := LoadTrust(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "keys"), 0o755); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(keys))
	for _, key := range keys {
		id := KeyID(key)
		if trust.Revoked[id] {
			return nil, fmt.Errorf("key %s is revoked in %s", id, filepath.Join(dir, "revoked"))
		}
		data, err := MarshalPublicKey(key)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(keyPath(dir, id), data, 0o644); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func keyPath(dir, id string) string {
	return filepath.Join(dir, "keys", strings.TrimPrefix(id, "ed25519:")+".pub")
}

// RevokeKey records id as revoked and removes its trusted key file. Revocation
// is permanent in the store: removing the line is the only way back.
func RevokeKey(dir, id string) error {
	if !validKeyID(id) {
		return fmt.Errorf("%q is not a Piglet key ID (ed25519:<32 hex>)", id)
	}
	trust, err := LoadTrust(dir)
	if err != nil {
		return err
	}
	if err := os.Remove(keyPath(dir, id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if trust.Revoked[id] {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(dir, "revoked"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(id + "\n"); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// SetRequireSignature turns the require-signature policy on or off.
func SetRequireSignature(dir string, on bool) error {
	path := Trust{Dir: dir}.requirePath()
	if !on {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("Every Piglet Binary must carry a signature by a key in keys/.\n"), 0o644)
}

// SortedKeyIDs lists the trusted key IDs in stable order.
func (t Trust) SortedKeyIDs() []string {
	ids := make([]string, 0, len(t.Keys))
	for id := range t.Keys {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}
