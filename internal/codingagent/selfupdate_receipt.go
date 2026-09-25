package codingagent

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/ownerfile"
)

const standaloneReceiptName = "install-receipt"

// InstalledPigVersion is the running Pig distribution version. cmd/pig sets it
// before dispatch so receipt validation can bind the executable to its release.
var InstalledPigVersion string

// StandaloneReceipt binds a standalone install to the exact executable,
// release, update source, and installed bytes. The installer writes this
// owner-only receipt; Pig only validates it.
type StandaloneReceipt struct {
	Kind              string
	ExecutablePath    string
	PigVersion        string
	SHA256            string
	UpdateSource      string
	UpdateTransportCA string
}

func standaloneReceiptPath() string {
	return filepath.Join(ConfigRoot(), standaloneReceiptName)
}

// WriteStandaloneReceipt atomically records a verified standalone install.
// Installers create the initial receipt; a successful standalone update
// replaces it with the newly installed release and digest.
func WriteStandaloneReceipt(exe, version, source string) error {
	if strings.ContainsAny(exe+version+source, "\r\n") || exe == "" || version == "" || source == "" {
		return fmt.Errorf("standalone receipt fields must be nonempty single-line values")
	}
	exe = canonicalPath(exe)
	file, err := os.Open(exe)
	if err != nil {
		return fmt.Errorf("hash standalone executable %s: %w", exe, err)
	}
	digest := sha256.New()
	_, copyErr := io.Copy(digest, file)
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("hash standalone executable %s: %w", exe, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close standalone executable %s: %w", exe, closeErr)
	}
	path := standaloneReceiptPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create standalone receipt directory: %w", err)
	}
	tmp, err := ownerfile.CreateTemp(filepath.Dir(path), ".install-receipt-*")
	if err != nil {
		return fmt.Errorf("stage standalone receipt: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	transportCA := ""
	if data, err := readUpdateTransportCA(); err != nil {
		return err
	} else if data != nil {
		sum := sha256.Sum256(data)
		transportCA = hex.EncodeToString(sum[:])
	}
	content := fmt.Sprintf(
		"kind=standalone\nexecutable=%s\npig-version=%s\nsha256=%s\nupdate-source=%s\n",
		exe, version, hex.EncodeToString(digest.Sum(nil)), source,
	)
	if transportCA != "" {
		content += "update-ca-sha256=" + transportCA + "\n"
	}
	if _, err := io.WriteString(tmp, content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write standalone receipt: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync standalone receipt: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close standalone receipt: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace standalone receipt: %w", err)
	}
	return nil
}

func readStandaloneReceipt() (*StandaloneReceipt, error) {
	path := standaloneReceiptPath()
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read standalone receipt %s: %w", path, err)
	}
	if ownerOnly, err := ownerfile.OwnerOnly(path, info); err != nil || !ownerOnly {
		return nil, fmt.Errorf("standalone receipt %s must be owner-only", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read standalone receipt %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	values := map[string]string{}
	scanner := bufio.NewScanner(io.LimitReader(file, 64<<10))
	for scanner.Scan() {
		line := scanner.Text()
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" || value == "" {
			return nil, fmt.Errorf("standalone receipt %s has malformed entry", path)
		}
		switch key {
		case "kind", "executable", "pig-version", "sha256", "update-source", "update-ca-sha256":
		default:
			return nil, fmt.Errorf("standalone receipt %s has unknown field %q", path, key)
		}
		if _, exists := values[key]; exists {
			return nil, fmt.Errorf("standalone receipt %s repeats field %q", path, key)
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read standalone receipt %s: %w", path, err)
	}
	receipt := &StandaloneReceipt{
		Kind:              values["kind"],
		ExecutablePath:    values["executable"],
		PigVersion:        values["pig-version"],
		SHA256:            values["sha256"],
		UpdateSource:      values["update-source"],
		UpdateTransportCA: values["update-ca-sha256"],
	}
	if receipt.Kind == "" || receipt.ExecutablePath == "" || receipt.PigVersion == "" || receipt.SHA256 == "" || receipt.UpdateSource == "" {
		return nil, fmt.Errorf("standalone receipt %s is incomplete", path)
	}
	return receipt, nil
}

func validateStandaloneReceipt(exe string) error {
	receipt, err := readStandaloneReceipt()
	if err != nil {
		return err
	}
	if receipt.Kind != "standalone" {
		return fmt.Errorf("standalone receipt kind is %q, want standalone", receipt.Kind)
	}
	if canonicalPath(receipt.ExecutablePath) != canonicalPath(exe) {
		return fmt.Errorf("standalone receipt executable %s does not match %s", receipt.ExecutablePath, exe)
	}
	if InstalledPigVersion == "" {
		return fmt.Errorf("running Pig release identity is unavailable")
	}
	if receipt.PigVersion != InstalledPigVersion {
		return fmt.Errorf("standalone receipt release %s does not match running Pig %s", receipt.PigVersion, InstalledPigVersion)
	}
	if receipt.UpdateSource != UpdateSourceURL() {
		return fmt.Errorf("standalone receipt update source %s does not match configured source %s", receipt.UpdateSource, UpdateSourceURL())
	}
	transportCA, err := readUpdateTransportCA()
	if err != nil {
		return err
	}
	if receipt.UpdateTransportCA == "" && transportCA != nil {
		return fmt.Errorf("standalone receipt does not own the configured update transport CA")
	}
	if receipt.UpdateTransportCA != "" {
		if transportCA == nil {
			return fmt.Errorf("standalone receipt update transport CA is missing")
		}
		if len(receipt.UpdateTransportCA) != sha256.Size*2 {
			return fmt.Errorf("standalone receipt has invalid update transport CA SHA256")
		}
		if _, err := hex.DecodeString(receipt.UpdateTransportCA); err != nil {
			return fmt.Errorf("standalone receipt has invalid update transport CA SHA256: %w", err)
		}
		sum := sha256.Sum256(transportCA)
		if !strings.EqualFold(receipt.UpdateTransportCA, hex.EncodeToString(sum[:])) {
			return fmt.Errorf("standalone receipt update transport CA digest does not match %s", filepath.Join(ConfigRoot(), updateTransportCASidecarName))
		}
	}
	if len(receipt.SHA256) != sha256.Size*2 {
		return fmt.Errorf("standalone receipt has invalid executable SHA256")
	}
	if _, err := hex.DecodeString(receipt.SHA256); err != nil {
		return fmt.Errorf("standalone receipt has invalid executable SHA256: %w", err)
	}
	file, err := os.Open(exe)
	if err != nil {
		return fmt.Errorf("hash standalone executable %s: %w", exe, err)
	}
	digest := sha256.New()
	_, copyErr := io.Copy(digest, file)
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("hash standalone executable %s: %w", exe, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close standalone executable %s: %w", exe, closeErr)
	}
	if !strings.EqualFold(receipt.SHA256, hex.EncodeToString(digest.Sum(nil))) {
		return fmt.Errorf("standalone receipt executable digest does not match %s", exe)
	}
	return nil
}
