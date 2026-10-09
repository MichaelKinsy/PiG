package signature

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/MichaelKinsy/PiG/internal/ownerfile"
)

// pig additive (D18): a signed Piglet Binary remembers a passing startup check so an unchanged Binary does not re-hash its executable bytes at every start.
const (
	// verifyCacheEntries bounds the cache to the most recently verified Binary paths.
	verifyCacheEntries = 8
	// maxVerifyCacheBytes bounds the cache file read at startup; a larger file is treated as corrupt.
	maxVerifyCacheBytes = 64 << 10
)

// fileIdentity is the file system identity of one executable: device and inode, or on Windows the volume serial number and file index; size; and modification and status-change times, or on Windows the last write and change times, in the platform's time units. Replacing the file, or writing to it through a file system that records a status-change time, changes at least one field.
type fileIdentity struct {
	Device   uint64 `json:"device"`
	Inode    uint64 `json:"inode"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
	Changed  int64  `json:"changed"`
}

// verifyCacheEntry records that the executable at Path, with Identity and signature block digest Block, passed a full check under the policy with digest Policy.
type verifyCacheEntry struct {
	Path     string       `json:"path"`
	Identity fileIdentity `json:"identity"`
	Block    string       `json:"block"`
	Policy   string       `json:"policy"`
}

// verifyCacheFile lists entries newest first, at most one per path. An entry vouches only for one file identity, and the verification rules ship inside that file, so a Binary with different rules is a different identity and never matches an older entry.
type verifyCacheFile struct {
	Entries []verifyCacheEntry `json:"entries"`
}

// CachedCheck is the outcome of CheckCached.
type CachedCheck struct {
	Status Status
	// remember is the entry a passing full check records; nil after a cache hit or a failed check.
	remember  *verifyCacheEntry
	identErr  error
	cachePath string
}

// CheckCached verifies the signature block of the executable at path under policy, as Check does, except for one step: when the verification cache at cachePath records a passing full check of the same resolved path, file identity, signature block, and policy, it does not hash the executable bytes again. It still verifies the envelope signature, the signed size, and the signer under policy. A missing, corrupt, unreadable, or not owner-only cache means a full check. A cache entry never vouches for another path. Only Piglet Binary startup uses it: explicit verification commands call Check.
func CheckCached(path string, policy Policy, cachePath string) (CachedCheck, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return CachedCheck{}, err
	}
	file, err := os.Open(resolved)
	if err != nil {
		return CachedCheck{}, err
	}
	defer func() { _ = file.Close() }() // Read-only: close cannot lose data.
	info, err := file.Stat()
	if err != nil {
		return CachedCheck{}, err
	}
	identity, identErr := identify(file, info)
	entry := verifyCacheEntry{Path: resolved, Identity: identity, Policy: policy.cacheDigest()}
	remembered := func(block signedBlock) bool {
		entry.Block = block.digest
		return identErr == nil && readVerifyCache(cachePath).holds(entry)
	}
	status, hashed, err := checkOpened(file, info.Size(), policy, remembered)
	check := CachedCheck{Status: status, cachePath: cachePath}
	if err != nil || !status.Signed || !hashed {
		return check, err
	}
	if identErr == nil {
		// A file that changed while it was hashed is not remembered: its next start hashes it again.
		after, err := file.Stat()
		if err != nil {
			identErr = err
		} else if identity, err := identify(file, after); err != nil || identity != entry.Identity {
			return check, nil
		}
	}
	check.remember, check.identErr = &entry, identErr
	return check, nil
}

// Remember records the passing full check so the next CheckCached of the unchanged file skips the hash. It does nothing after a cache hit, a failed check, or an unsigned file. Call it only after every other check of the file passed. The cache is replaced atomically and is readable and writable by its owner only.
func (c CachedCheck) Remember() error {
	if c.remember == nil {
		return nil
	}
	if c.identErr != nil {
		return fmt.Errorf("identify %s for the Piglet verification cache: %w", c.remember.Path, c.identErr)
	}
	cache := readVerifyCache(c.cachePath)
	entries := []verifyCacheEntry{*c.remember}
	for _, entry := range cache.Entries {
		if entry.Path != c.remember.Path && len(entries) < verifyCacheEntries {
			entries = append(entries, entry)
		}
	}
	data, err := json.Marshal(verifyCacheFile{Entries: entries})
	if err != nil {
		return err
	}
	if err := writeOwnerOnly(c.cachePath, data); err != nil {
		return fmt.Errorf("write Piglet verification cache: %w", err)
	}
	return nil
}

func (c verifyCacheFile) holds(want verifyCacheEntry) bool {
	return slices.Contains(c.Entries, want)
}

// readVerifyCache returns the cache at path, or an empty cache when it is missing, unreadable, too large, not owner-only, or malformed.
func readVerifyCache(path string) verifyCacheFile {
	file, err := os.Open(path)
	if err != nil {
		return verifyCacheFile{}
	}
	defer func() { _ = file.Close() }() // Read-only: close cannot lose data.
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxVerifyCacheBytes {
		return verifyCacheFile{}
	}
	if ownerOnly, err := ownerfile.OwnerOnly(path, info); err != nil || !ownerOnly {
		return verifyCacheFile{}
	}
	data, err := io.ReadAll(io.LimitReader(file, maxVerifyCacheBytes+1))
	if err != nil || len(data) > maxVerifyCacheBytes {
		return verifyCacheFile{}
	}
	var cache verifyCacheFile
	if err := strictJSON(data, &cache); err != nil {
		return verifyCacheFile{}
	}
	return cache
}

// writeOwnerOnly replaces path with data through an owner-only temporary file in the same directory. It does not sync: a cache lost or torn by a crash reads as corrupt, which only costs one full check.
func writeOwnerOnly(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := ownerfile.CreateTemp(dir, ".verified-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_, writeErr := tmp.Write(data)
	if err := errors.Join(writeErr, tmp.Close()); err != nil {
		return errors.Join(err, os.Remove(tmpPath))
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return errors.Join(err, os.Remove(tmpPath))
	}
	return nil
}

// cacheDigest identifies everything in p that decides a check: the signer requirements and every trusted, revoked, and embedded key.
func (p Policy) cacheDigest() string {
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "pig-piglet-verify-cache\nrequire-known-signer=%t\nrequire-signature=%t\n", p.RequireKnownSigner, p.Trust.RequireSignature)
	for _, id := range p.Trust.SortedKeyIDs() {
		_, _ = fmt.Fprintf(hash, "trusted=%s\n", base64.StdEncoding.EncodeToString(p.Trust.Keys[id]))
	}
	for _, id := range slices.Sorted(maps.Keys(p.Trust.Revoked)) {
		_, _ = fmt.Fprintf(hash, "revoked=%s\n", id)
	}
	embedded := make([]string, 0, len(p.Embedded))
	for _, key := range p.Embedded {
		embedded = append(embedded, base64.StdEncoding.EncodeToString(key))
	}
	slices.Sort(embedded)
	for _, key := range embedded {
		_, _ = fmt.Fprintf(hash, "embedded=%s\n", key)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}
