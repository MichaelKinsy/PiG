package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports the part of apps/p2/src/runtime/session-files.ts the files plugin uses: a session's files in its worker's `Files`
// (the app kit's, one per host): the spaces p2 writes, whole reads of a version, and the download snapshots producers save.

// ArtifactsSpace is the space of a session's artifacts: path = the artifact's name, Files versions = its versions.
const ArtifactsSpace = "artifacts"

// UploadsSpace is the space of a session's uploads (POST /upload): path `<id>-<name>`, key = the upload's id.
const UploadsSpace = "uploads"

// DownloadsSpace is the space of a session's download snapshots (send_file): path `<id>-<name>`, key = the download's id.
const DownloadsSpace = "downloads"

// MaxDownloadBytes is the largest download snapshot: 25 MiB.
const MaxDownloadBytes = 25 * 1024 * 1024

// FileChunk is the largest piece `Files` takes or gives in one call.
const FileChunk = 1024 * 1024

// DownloadError is a download or snapshot refused with an HTTP status.
type DownloadError struct {
	Status  int
	Message string
}

func (err *DownloadError) Error() string { return err.Message }

// ReadWhole is the whole bytes of version file (read a chunk at a time).
func ReadWhole(files *host.Reference, file *delta.JsonObject, ctx context.Context) ([]byte, error) {
	size := int(file.Value("size").(float64))
	whole := make([]byte, 0, size)
	for len(whole) < size {
		value, err := files.Call("read", delta.JsonObjectOf("id", file.Value("id"), "offset", float64(len(whole)), "length", float64(FileChunk)), ctx)
		if err != nil {
			return nil, err
		}
		bytes, _ := value.([]byte)
		if len(bytes) == 0 {
			return nil, fmt.Errorf("File %v changed while read", file.Value("path"))
		}
		whole = append(whole, bytes...)
	}
	return whole, nil
}

// Download is a download snapshot found in the files: its version and display name.
type Download struct {
	File *delta.JsonObject
	Name string
}

// FindDownload is the download snapshot id of the session (nil for anything else) and its display name.
func FindDownload(files *host.Reference, id string, ctx context.Context) (*Download, error) {
	if id == "" {
		return nil, nil
	}
	found, err := files.Call("byKey", delta.JsonObjectOf("space", DownloadsSpace, "key", id), ctx)
	if err != nil {
		return nil, err
	}
	file, _ := found.(*delta.JsonObject)
	if file == nil {
		return nil, nil
	}
	return &Download{File: file, Name: DisplayName(file)}, nil
}

// DisplayName is a file's display name: `meta.name` as p2 writes it, else its path's last segment.
func DisplayName(file *delta.JsonObject) string {
	if meta, ok := file.Value("meta").(*delta.JsonObject); ok {
		if name, ok := meta.Value("name").(string); ok {
			return name
		}
	}
	path, _ := file.Value("path").(string)
	return path[strings.LastIndexByte(path, '/')+1:]
}
