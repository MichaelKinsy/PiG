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
	"io/fs"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/internal/archivepath"
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
//
// Link names are text, but the kernel resolves them against the tree being
// written: "z/.." climbs one directory more than it reads when z is a link to
// ".". So no entry may be located through a link the archive wrote, judged on
// disk (requireRealFolders) because a file system may fold one name onto
// another (case, Unicode normalization), a link target climbs only before it
// names anything (archivepath.CheckLink), and every write goes through an
// os.Root, which refuses any resolution that leaves root.
func untarNodeSources(data []byte, root string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	tree, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = tree.Close() }()
	real := map[string]struct{}{}
	reader := tar.NewReader(bytes.NewReader(data))
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name, err := archivepath.Member(header.Name)
		if err != nil {
			return err
		}
		if err := requireRealFolders(tree, real, name, header.Typeflag == tar.TypeDir); err != nil {
			return fmt.Errorf("archive entry %s: %w", header.Name, err)
		}
		local := filepath.FromSlash(name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := tree.MkdirAll(local, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := tree.MkdirAll(filepath.Dir(local), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if header.Mode&0o111 != 0 {
				mode = 0o755
			}
			file, err := tree.OpenFile(local, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(file, reader)
			if err := errors.Join(copyErr, file.Close()); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := archivepath.CheckLink(name, header.Linkname); err != nil {
				return err
			}
			if err := tree.MkdirAll(filepath.Dir(local), 0o755); err != nil {
				return err
			}
			if err := tree.Symlink(filepath.FromSlash(header.Linkname), local); err != nil {
				return err
			}
		default:
			return fmt.Errorf("archive entry %s has unsupported type %q", header.Name, header.Typeflag)
		}
	}
}

// requireRealFolders refuses an entry located through a symbolic link: it
// Lstats each folder on the way to name (and name itself for a folder entry,
// which MkdirAll would accept as an existing link to a folder), nearest to the
// root first so each Lstat resolves through folders already confirmed real.
// Folders that do not exist yet are the extraction's to create. real holds the
// folders confirmed so far, which stay real: a folder cannot become a link.
func requireRealFolders(tree *os.Root, real map[string]struct{}, name string, includeName bool) error {
	for end := 0; end <= len(name); end++ {
		atSeparator := end < len(name) && name[end] == '/'
		atName := end == len(name) && includeName
		if !atSeparator && !atName {
			continue
		}
		folder := name[:end]
		if _, ok := real[folder]; ok {
			continue
		}
		info, err := tree.Lstat(filepath.FromSlash(folder)) //perf:ok once per distinct folder of a Node cell archive being unpacked at install, memoised in real
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			return fmt.Errorf("is located through the link %s", folder)
		case !info.IsDir():
			return fmt.Errorf("is located under %s, which is not a folder", folder)
		}
		real[folder] = struct{}{}
	}
	return nil
}
