package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Installed is one verified pulled release receipt and managed artifact.
type Installed struct {
	ReceiptPath  string
	ArtifactPath string
	CurrentPath  string
	Index        Index
	Manifest     signature.Manifest
	Digest       string
	Size         int64
}

// ListInstalled verifies every pulled release receipt, its signed artifact,
// and each Piglet's atomic current pointer.
func ListInstalled() ([]Installed, []error) {
	root, err := openStore("receipts", false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, []error{fmt.Errorf("inspect Piglet receipt store: %w", err)}
	}
	defer func() { _ = root.Close() }()
	var installed []Installed
	var errs []error
	walkErr := fs.WalkDir(root.FS(), ".", func(relative string, entry os.DirEntry, walkErr error) error {
		path := filepath.Join(codingagent.PigletRecordsDir(), filepath.FromSlash(relative))
		if walkErr != nil {
			errs = append(errs, fmt.Errorf("read Piglet receipt path %s: %w", path, walkErr))
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			errs = append(errs, fmt.Errorf("Piglet receipt path %s is a symlink; managed receipts must be regular files", path))
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || filepath.Ext(path) != ".pull" {
			return nil
		}
		item, err := readInstalled(path)
		if err != nil {
			errs = append(errs, err)
			return nil
		}
		installed = append(installed, item)
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, os.ErrNotExist) {
		errs = append(errs, walkErr)
	}
	currents := make(map[string]currentPointer)
	for i := range installed {
		piglet := installed[i].Index.Piglet
		if _, exists := currents[piglet]; !exists {
			current, err := readCurrent(piglet)
			if err != nil {
				errs = append(errs, fmt.Errorf("read current Piglet release for %s: %w", piglet, err))
				continue
			}
			currents[piglet] = current
		}
		installed[i].CurrentPath = currentPath(piglet)
	}
	for piglet, current := range currents {
		if err := validateCurrent(piglet, current, installed); err != nil {
			errs = append(errs, err)
		}
	}
	return installed, errs
}

func readInstalled(path string) (Installed, error) {
	relative, err := filepath.Rel(codingagent.PigletRecordsDir(), path)
	if err != nil {
		return Installed{}, err
	}
	data, err := readManagedFile("receipts", relative)
	if err != nil {
		return Installed{}, fmt.Errorf("read Piglet release receipt %s: %w", path, err)
	}
	var receipt Receipt
	if err := strictJSON(data, &receipt); err != nil {
		return Installed{}, fmt.Errorf("Piglet release receipt %s: %w", path, err)
	}
	installedAt, err := time.Parse(time.RFC3339, receipt.InstalledAt)
	if err != nil || installedAt.UTC().Format(time.RFC3339) != receipt.InstalledAt {
		return Installed{}, fmt.Errorf("Piglet release receipt %s has invalid installedAt %q", path, receipt.InstalledAt)
	}
	envelopeData, err := json.Marshal(receipt.IndexEnvelope)
	if err != nil {
		return Installed{}, err
	}
	verified, err := Verify(envelopeData)
	if err != nil {
		return Installed{}, fmt.Errorf("Piglet release receipt %s: %w", path, err)
	}
	if !validRelativePath(receipt.Artifact.Path) || !validDigest(receipt.Artifact.Digest) || receipt.Artifact.Size <= 0 {
		return Installed{}, fmt.Errorf("Piglet release receipt %s has invalid artifact identity", path)
	}
	indexedArtifact, ok := verified.Index.Binaries[receipt.Manifest.Target]
	if !ok || receipt.Artifact.Digest != "sha256:"+indexedArtifact.SHA256 || receipt.Artifact.Size != indexedArtifact.Size {
		return Installed{}, fmt.Errorf("Piglet release receipt %s artifact does not match its signed release index", path)
	}
	artifactPath := filepath.Join(codingagent.PigletArtifactsDir(), filepath.FromSlash(receipt.Artifact.Path))
	if err := validateReceiptPath(path, verified.Index, receipt.Manifest, artifactPath); err != nil {
		return Installed{}, err
	}
	file, err := openManagedFile("artifacts", filepath.FromSlash(receipt.Artifact.Path))
	if err != nil {
		return Installed{}, err
	}
	defer func() { _ = file.Close() }()
	if err := verifyArtifact(file, receipt.Artifact.Digest, receipt.Artifact.Size); err != nil {
		return Installed{}, err
	}
	status, err := signature.CheckFile(file, signature.Policy{})
	if err != nil {
		return Installed{}, fmt.Errorf("Piglet release receipt %s artifact: %w", path, err)
	}
	if !status.Signed {
		return Installed{}, fmt.Errorf("Piglet release receipt %s artifact is unsigned", path)
	}
	if !reflect.DeepEqual(status.Manifest, receipt.Manifest) {
		return Installed{}, fmt.Errorf("Piglet release receipt %s manifest does not match its artifact", path)
	}
	if err := matchBinaryManifest(status, verified, receipt.Manifest.Target); err != nil {
		return Installed{}, fmt.Errorf("Piglet release receipt %s: %w", path, err)
	}
	return Installed{
		ReceiptPath: path, ArtifactPath: artifactPath, Index: verified.Index,
		Manifest: receipt.Manifest, Digest: receipt.Artifact.Digest, Size: receipt.Artifact.Size,
	}, nil
}

func validateReceiptPath(path string, index Index, manifest signature.Manifest, artifactPath string) error {
	goos, goarch, ok := strings.Cut(manifest.Target, "/")
	if !ok {
		return fmt.Errorf("Piglet release receipt %s has invalid target %q", path, manifest.Target)
	}
	expectedReceipt := filepath.Join(codingagent.PigletRecordsDir(), index.Piglet, index.Version, goos, goarch+".pull")
	if path != expectedReceipt {
		return fmt.Errorf("Piglet release receipt %s is stored at the wrong path; expected %s", path, expectedReceipt)
	}
	name := "pig-" + index.Piglet
	if goos == "windows" {
		name += ".exe"
	}
	expectedArtifact := filepath.Join(codingagent.PigletArtifactsDir(), index.Piglet, index.Version, goos, goarch, name)
	if artifactPath != expectedArtifact {
		return fmt.Errorf("Piglet release receipt %s names artifact %s, expected %s", path, artifactPath, expectedArtifact)
	}
	return nil
}

func verifyArtifact(file *os.File, expectedDigest string, expectedSize int64) error {
	path := file.Name()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != expectedSize {
		return fmt.Errorf("pulled Piglet Binary %s size/type does not match its receipt", path)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, io.NewSectionReader(file, 0, info.Size())); err != nil {
		return err
	}
	digest := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	if digest != expectedDigest {
		return fmt.Errorf("pulled Piglet Binary %s digest %s does not match its receipt %s", path, digest, expectedDigest)
	}
	return nil
}

func validateCurrent(piglet string, current currentPointer, installed []Installed) error {
	artifact := filepath.Join(codingagent.PigletArtifactsDir(), filepath.FromSlash(current.Artifact))
	receipt := filepath.Join(codingagent.PigletRecordsDir(), filepath.FromSlash(current.Receipt))
	for _, item := range installed {
		if item.Index.Piglet == piglet && item.ArtifactPath == artifact && item.ReceiptPath == receipt && item.Index.Signer.KeyID == current.SignerKeyID {
			return nil
		}
	}
	return fmt.Errorf("current Piglet release for %s does not name an installed artifact, receipt, and signer", piglet)
}
