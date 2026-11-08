package files

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports apps/p2/plugins/files/uploads.ts: uploads of a session (POST /upload): their limit, their image types, and the
// materialized copies the model reads. The bytes live in the session's `Files` (space `uploads`); a message's attachments
// are written as plain files to the machine where the receiving conversation works before the message is admitted.

// MaxUploadBytes is the largest upload: 25 MiB.
const MaxUploadBytes = 25 * 1024 * 1024

var imageTypes = map[string]string{
	"png": "image/png", "jpg": "image/jpeg", "jpeg": "image/jpeg", "gif": "image/gif", "webp": "image/webp",
}

// ImageType is the image type of a file name (images may go to the model inline), else "" (the text after the last dot,
// or the whole name without one, as `name.split(".").pop()` is).
func ImageType(name string) string {
	return imageTypes[strings.ToLower(name[strings.LastIndexByte(name, '.')+1:])]
}

// UploadPath is an upload's path in the space `uploads`, and its materialized copies' file name: `<id>-<safe name>`.
func UploadPath(id, name string) string { return id + "-" + p2runtime.SafeFileName(name) }

// MaterializeLocally writes upload file (path `file.path`) into the local directory (mode 0700, file 0600) unless a file of
// its size is there: staged and renamed, so a reader never sees half of it. Its path.
func MaterializeLocally(files *host.Reference, file *delta.JsonObject, directory string, ctx context.Context) (string, error) {
	path := filepath.Join(directory, file.Value("path").(string))
	size := int64(file.Value("size").(float64))
	if existing, err := os.Stat(path); err == nil && existing.Mode().IsRegular() && existing.Size() == size {
		return path, nil
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	staging := fmt.Sprintf("%s.%d.partial", path, os.Getpid())
	output, err := os.OpenFile(staging, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	fail := func(err error) (string, error) {
		_ = output.Close()
		_ = os.Remove(staging)
		return "", err
	}
	for offset := int64(0); offset < size; {
		value, err := files.Call("read", delta.JsonObjectOf("id", file.Value("id"), "offset", float64(offset), "length", float64(p2runtime.FileChunk)), ctx)
		if err != nil {
			return fail(err)
		}
		bytes, _ := value.([]byte)
		if len(bytes) == 0 {
			return fail(fmt.Errorf("Upload %v changed while read", file.Value("path")))
		}
		if _, err := output.Write(bytes); err != nil {
			return fail(err)
		}
		offset += int64(len(bytes))
	}
	if err := output.Close(); err != nil {
		_ = os.Remove(staging)
		return "", err
	}
	if err := os.Rename(staging, path); err != nil {
		_ = os.Remove(staging)
		return "", err
	}
	return path, nil
}

// UploadError is an upload refused with an HTTP status.
type UploadError struct {
	Status  int
	Message string
}

func (err *UploadError) Error() string { return err.Message }
