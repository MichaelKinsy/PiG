package pigletbuild

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension/host/cellpack"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// buildNodeCell packs the sources of a Node cell's members into one tar
// archive that the Piglet Binary embeds. Nothing is compiled: at startup the
// Binary extracts the sources and runs them with the user's installed Node
// through the same Node cell path Stock PiG uses, so the extension behaves as
// it does under Stock PiG. A directory member is archived whole except its
// .git metadata; a single-file member is archived as that file.
// pig additive (D18): a Piglet Binary carries its TypeScript and JavaScript extensions.
func buildNodeCell(cell subprocess.CellSpec, cacheRoot string, host Target) (stagedCell, error) {
	dir := filepath.Join(cacheRoot, "node")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return stagedCell{}, err
	}
	file, err := os.CreateTemp(dir, "cell-*.tar")
	if err != nil {
		return stagedCell{}, err
	}
	archivePath := file.Name()
	hash := sha256.New()
	writer := tar.NewWriter(io.MultiWriter(file, hash))
	exts := make([]cellpack.ExtEntry, len(cell.Extensions))
	for i, cfg := range cell.Extensions {
		source, err := writeNodeMember(writer, cfg)
		if err != nil {
			_ = file.Close()
			return stagedCell{}, fmt.Errorf("pack Node extension %q: %w", cfg.Name, err)
		}
		exts[i] = cellpack.ExtEntry{Name: cfg.Name, Hash: extHash(cfg), Source: source}
	}
	if err := errors.Join(writer.Close(), file.Close()); err != nil {
		return stagedCell{}, err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	return stagedCell{
		entry: cellpack.CellEntry{
			Language: "node", Key: cell.Key, Strategy: string(cell.Strategy), OS: host.OS, Arch: host.Arch,
			Binary: "node/" + shortHash(digest) + ".tar", Digest: digest, Extensions: exts,
		},
		binaryPath: archivePath,
	}, nil
}

// writeNodeMember archives one member's source under its name and returns the
// member's source path inside the archive.
func writeNodeMember(writer *tar.Writer, cfg subprocess.ExtConfig) (string, error) {
	if cfg.Source == "" {
		return "", fmt.Errorf("extension has no source to embed")
	}
	if cfg.Name == "" || strings.ContainsAny(cfg.Name, `/\`) || cfg.Name == "." || cfg.Name == ".." {
		return "", fmt.Errorf("extension name %q cannot name an archive directory", cfg.Name)
	}
	absolute, err := filepath.Abs(cfg.Source)
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if err := writeArchiveDir(writer, cfg.Name); err != nil {
		return "", err
	}
	if !info.IsDir() {
		member := cfg.Name + "/" + filepath.Base(absolute)
		return member, writeArchiveFile(writer, member, root, info)
	}
	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if current == root {
			return nil
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		name := cfg.Name + "/" + filepath.ToSlash(relative)
		switch {
		case entry.IsDir():
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return writeArchiveDir(writer, name)
		case entry.Type()&fs.ModeSymlink != 0:
			return writeArchiveLink(writer, name, root, current)
		case entry.Type().IsRegular():
			info, err := entry.Info()
			if err != nil {
				return err
			}
			return writeArchiveFile(writer, name, current, info)
		default:
			return fmt.Errorf("%s is not a regular file, directory, or symbolic link", current)
		}
	})
	return cfg.Name, err
}

func writeArchiveDir(writer *tar.Writer, name string) error {
	return writer.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: name + "/", Mode: 0o755, Format: tar.FormatPAX})
}

func writeArchiveFile(writer *tar.Writer, name, source string, info fs.FileInfo) error {
	mode := int64(0o644)
	if info.Mode()&0o111 != 0 {
		mode = 0o755
	}
	if err := writer.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: mode, Size: info.Size(), Format: tar.FormatPAX}); err != nil {
		return err
	}
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	_, copyErr := io.CopyN(writer, file, info.Size())
	return errors.Join(copyErr, file.Close())
}

// writeArchiveLink keeps a symbolic link whose relative target stays inside
// the member's directory or names the directory itself, such as a package
// manager's node_modules link or a package's link to itself. A link that
// leaves the directory cannot be embedded.
func writeArchiveLink(writer *tar.Writer, name, root, current string) error {
	target, err := os.Readlink(current)
	if err != nil {
		return err
	}
	slashTarget := filepath.ToSlash(target)
	member, _, _ := strings.Cut(name, "/")
	//portlint:allow pathseparators tar member and link names are slash paths by the tar format, not host paths
	resolved := path.Join(path.Dir(name), slashTarget)
	if filepath.IsAbs(target) || (resolved != member && !strings.HasPrefix(resolved, member+"/")) {
		return fmt.Errorf("symbolic link %s points outside the extension directory %s; the Piglet Binary embeds only that directory", current, root)
	}
	return writer.WriteHeader(&tar.Header{Typeflag: tar.TypeSymlink, Name: name, Linkname: slashTarget, Mode: 0o777, Format: tar.FormatPAX})
}
