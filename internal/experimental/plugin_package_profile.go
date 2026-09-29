// Ports packages/coding-agent/src/experimental/plugins/package.ts.
package experimental

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// RestoreServerPluginPackageProfile persists an explicit package selection or restores it for a later server generation. Nil means omitted; a non-nil empty slice deletes the server selection file and rejects a directory at that path.
func RestoreServerPluginPackageProfile(directory, serverId string, configuredPackagePaths []string) ([]string, error) {
	path := filepath.Join(directory, "plugin-packages-"+serverId+".json")
	if configuredPackagePaths == nil {
		paths, err := readPluginPackageProfile(path, false, nil)
		if paths == nil && err == nil {
			paths = []string{}
		}
		return paths, err
	}
	paths, err := NormalizePluginPackagePaths(configuredPackagePaths)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		err = removePluginPackageProfile(path)
	} else {
		err = writePluginPackageProfile(path, paths, nil)
	}
	if err != nil {
		return nil, err
	}
	return paths, nil
}

// ReadSessionPluginPackageProfile reads the selection for one durable Session. Nil means the profile does not exist; an empty slice means an explicitly empty selection.
func ReadSessionPluginPackageProfile(directory, serverId, sessionPath string) ([]string, error) {
	return readPluginPackageProfile(sessionPluginProfilePath(directory, serverId, sessionPath), true, &sessionPath)
}

// RemoveSessionPluginPackageProfile removes a deleted Session's selection. A missing file is already removed.
func RemoveSessionPluginPackageProfile(directory, serverId, sessionPath string) error {
	return removePluginPackageProfile(sessionPluginProfilePath(directory, serverId, sessionPath))
}

// WriteSessionPluginPackageProfile persists normalized package paths for one Session, including an explicitly empty selection.
func WriteSessionPluginPackageProfile(directory, serverId, sessionPath string, packagePaths []string) error {
	paths, err := NormalizePluginPackagePaths(packagePaths)
	if err != nil {
		return err
	}
	return writePluginPackageProfile(sessionPluginProfilePath(directory, serverId, sessionPath), paths, &sessionPath)
}

func readPluginPackageProfile(path string, allowEmpty bool, sessionPath *string) ([]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("Could not read experimental plugin package profile %s: %w", path, err)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("Could not read experimental plugin package profile %s: %w", path, err)
	}
	invalid := func() ([]string, error) {
		return nil, fmt.Errorf("Invalid experimental plugin package profile %s", path)
	}
	profile, ok := value.(map[string]any)
	if !ok {
		return invalid()
	}
	for key := range profile {
		if key != "version" && key != "packagePaths" && (key != "sessionPath" || sessionPath == nil) {
			return invalid()
		}
	}
	// Upstream owns this exact profile version in plugins/package.ts:12,118.
	if version, ok := profile["version"].(float64); !ok || version != 1 {
		return invalid()
	}
	if sessionPath != nil {
		stored, ok := profile["sessionPath"].(string)
		if !ok || stored != *sessionPath {
			return invalid()
		}
	}
	values, ok := profile["packagePaths"].([]any)
	if !ok || (!allowEmpty && len(values) == 0) {
		return invalid()
	}
	paths := make([]string, len(values))
	for i, value := range values {
		path, ok := value.(string)
		if !ok || path == "" {
			return invalid()
		}
		paths[i] = path
	}
	return NormalizePluginPackagePaths(paths)
}

func writePluginPackageProfile(path string, packagePaths []string, sessionPath *string) error {
	profile := struct {
		Version      int      `json:"version"`
		SessionPath  *string  `json:"sessionPath,omitempty"`
		PackagePaths []string `json:"packagePaths"`
	}{Version: 1, SessionPath: sessionPath, PackagePaths: packagePaths}
	data, err := json.Marshal(profile)
	if err != nil {
		return err
	}
	data, err = jsonstringify.Canonicalize(data)
	if err != nil {
		return err
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, data, "", "  "); err != nil {
		return err
	}
	formatted.WriteByte('\n')
	return os.WriteFile(path, formatted.Bytes(), 0o600)
}

// NormalizePluginPackagePaths resolves paths against the current working directory, preserves order, and rejects empty or duplicate paths.
func NormalizePluginPackagePaths(packagePaths []string) ([]string, error) {
	paths := make([]string, len(packagePaths))
	for i, path := range packagePaths {
		if path == "" {
			return nil, errors.New("Plugin package path must not be empty")
		}
		resolved, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		paths[i] = resolved
	}
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		if seen[path] {
			return nil, errors.New("Plugin package paths must be unique")
		}
		seen[path] = true
	}
	return paths, nil
}

func sessionPluginProfilePath(directory, serverId, sessionPath string) string {
	digest := sha256.Sum256([]byte(sessionPath))
	return filepath.Join(directory, "session-plugin-packages-"+serverId+"-"+hex.EncodeToString(digest[:])[:24]+".json")
}

func removePluginPackageProfile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	// Upstream rm(path, { force: true }) does not enable recursive directory removal.
	if info.IsDir() {
		return &os.PathError{Op: "remove", Path: path, Err: syscall.EISDIR}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
