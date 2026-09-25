package codingagent

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
)

// quarantineDirName is the directory, beside the nearest node_modules, that
// holds native images moved out of an installation npm is about to replace.
const quarantineDirName = ".pig-native-quarantine"

// GetPackageDir is the installation directory of a compiled pig: the
// directory holding the running executable, as upstream getPackageDir returns
// for a compiled binary. It returns "" when the executable cannot be located.
func GetPackageDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Dir(exe)
}

func getQuarantineRoot(packageDir string) (string, bool) {
	if packageDir == "" {
		return "", false
	}
	current, err := filepath.Abs(packageDir)
	if err != nil {
		return "", false
	}
	for {
		if strings.ToLower(filepath.Base(current)) == "node_modules" {
			return filepath.Join(current, quarantineDirName), true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", false
		}
		current = parent
	}
}

// loadedSharedObjectsInPackageDir returns the loaded images inside
// packageDir, compared case-insensitively and each listed once.
func loadedSharedObjectsInPackageDir(packageDir string, sharedObjects []string) []string {
	root := strings.ToLower(packageDir)
	seen := make(map[string]bool)
	var loadedFiles []string
	for _, value := range sharedObjects {
		filePath, err := filepath.Abs(value)
		if err != nil {
			continue
		}
		comparisonPath := strings.ToLower(filePath)
		if !pathWithin(comparisonPath, root) || seen[comparisonPath] {
			continue
		}
		seen[comparisonPath] = true
		loadedFiles = append(loadedFiles, filePath)
	}
	return loadedFiles
}

func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// CleanupWindowsSelfUpdateQuarantine removes the images an earlier npm
// self-update quarantined beside packageDir. A previous pig process may still
// be exiting and holding one, so a failure leaves the quarantine for the next
// start.
func CleanupWindowsSelfUpdateQuarantine(packageDir string) {
	quarantineRoot, ok := getQuarantineRoot(packageDir)
	if !ok {
		return
	}
	// upstream: packages/coding-agent/src/utils/windows-self-update.ts:cleanupWindowsSelfUpdateQuarantine
	_ = os.RemoveAll(quarantineRoot)
}

// QuarantineWindowsNativeDependencies moves each image this process loaded
// from packageDir into the quarantine and copies it back. Windows lets a
// loaded image be renamed but not overwritten or deleted, so the copy leaves
// npm free to replace packageDir while this process runs.
func QuarantineWindowsNativeDependencies(packageDir string) error {
	return quarantineNativeDependencies(packageDir, loadedSharedObjects())
}

func quarantineNativeDependencies(packageDir string, sharedObjects []string) error {
	resolvedPackageDir, err := filepath.Abs(packageDir)
	if err != nil {
		return err
	}
	quarantineRoot, ok := getQuarantineRoot(resolvedPackageDir)
	if !ok {
		return nil
	}
	loadedFiles := loadedSharedObjectsInPackageDir(resolvedPackageDir, sharedObjects)
	if len(loadedFiles) == 0 {
		return nil
	}
	quarantineRunDir := filepath.Join(quarantineRoot, fmt.Sprintf("%d-%d-%s", time.Now().UnixMilli(), os.Getpid(), uuid.NewString()))
	for _, loadedFile := range loadedFiles {
		if _, err := os.Stat(loadedFile); err != nil {
			continue
		}
		rel, err := filepath.Rel(resolvedPackageDir, loadedFile)
		if err != nil {
			return err
		}
		quarantinePath := filepath.Join(quarantineRunDir, rel)
		if err := os.MkdirAll(filepath.Dir(quarantinePath), 0o755); err != nil {
			return err
		}
		if err := os.Rename(loadedFile, quarantinePath); err != nil {
			return err
		}
		if err := copyQuarantinedImage(quarantinePath, loadedFile); err != nil {
			return err
		}
	}
	return nil
}

func copyQuarantinedImage(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// PrepareWindowsNpmSelfUpdate readies the npm installation holding exePath
// for npm to replace: it clears an earlier quarantine and quarantines the
// images this process loaded from it, which for pig is the running
// executable. It does nothing outside Windows.
func PrepareWindowsNpmSelfUpdate(exePath string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	packageDir := filepath.Dir(exePath)
	CleanupWindowsSelfUpdateQuarantine(packageDir)
	return QuarantineWindowsNativeDependencies(packageDir)
}
