package cellpack

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
)

// pig additive (D18): a Piglet Binary carries its TypeScript and JavaScript
// extensions as their sources; the host runs them with the user's installed
// Node exactly as Stock PiG runs the same extensions.

const nodeLanguage = "node"

// nodeSourcesName is the directory, inside a Node cell's extraction entry,
// that holds one subdirectory per member.
const nodeSourcesName = "sources"

// nodeArchiveMarker is the entry's published artifact: the archive digest.
const nodeArchiveMarker = "archive.sha256"

// nodeSourcesDir is where the Node cell archive with the given digest is
// extracted under dest.
func nodeSourcesDir(dest, digest string) string {
	return filepath.Join(dest, nodeLanguage, digest, nodeSourcesName)
}

// extractNodeCell verifies a Node cell archive against its manifest digest and
// publishes its sources once per digest. Publication is the content-addressed,
// atomic cache entry Stock PiG uses for its own Node runtime, so concurrent
// Binaries share one complete extraction and never observe a partial one.
func extractNodeCell(data []byte, c CellEntry, dest string) error {
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	if c.Digest != digest {
		return fmt.Errorf("archive digest %s does not match its manifest digest %q", digest, c.Digest)
	}
	entryDir := filepath.Dir(nodeSourcesDir(dest, digest))
	_, err := runtimecell.PublishArtifact(context.Background(), entryDir, nodeArchiveMarker, digest, nodeLanguage, func(scratch string) (string, error) {
		if err := untarNodeSources(data, filepath.Join(scratch, nodeSourcesName)); err != nil {
			return "", err
		}
		marker := filepath.Join(scratch, nodeArchiveMarker)
		return marker, os.WriteFile(marker, []byte(digest), 0o644)
	}, nodeSourcesName)
	return err
}

// untarNodeSources unpacks a Node cell archive into root. It accepts only
// directories, regular files, and relative symbolic links that stay inside
// root, so an archive cannot write or point outside its own tree.
func untarNodeSources(data []byte, root string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	reader := tar.NewReader(bytes.NewReader(data))
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name, err := archiveMemberPath(header.Name)
		if err != nil {
			return err
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if header.Mode&0o111 != 0 {
				mode = 0o755
			}
			file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(file, reader)
			if err := errors.Join(copyErr, file.Close()); err != nil {
				return err
			}
		case tar.TypeSymlink:
			//portlint:allow pathseparators tar member and link names are slash paths by the tar format, not host paths
			if path.IsAbs(header.Linkname) || strings.Contains(header.Linkname, `\`) {
				return fmt.Errorf("archive link %s has a non-relative target %q", header.Name, header.Linkname)
			}
			//portlint:allow pathseparators tar member and link names are slash paths by the tar format, not host paths
			if _, err := archiveMemberPath(path.Join(path.Dir(name), header.Linkname)); err != nil {
				return fmt.Errorf("archive link %s points outside its sources: %w", header.Name, err)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(filepath.FromSlash(header.Linkname), target); err != nil {
				return err
			}
		default:
			return fmt.Errorf("archive entry %s has unsupported type %q", header.Name, header.Typeflag)
		}
	}
}

// archiveMemberPath returns name as a clean relative slash path inside the
// archive root, or an error when it is absolute or escapes the root.
func archiveMemberPath(name string) (string, error) {
	//portlint:allow pathseparators tar member and link names are slash paths by the tar format, not host paths
	clean := path.Clean(name)
	//portlint:allow pathseparators tar member and link names are slash paths by the tar format, not host paths
	if name == "" || path.IsAbs(name) || strings.Contains(name, `\`) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("archive path %q is not inside the archive", name)
	}
	return clean, nil
}
