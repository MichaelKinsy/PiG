// Package noderuntimepack stores the embedded Node runtime as a few solid zstd blocks. The runtime's package copies repeat one another (an SDK bundle contains the modules it was built from), and a block compresses those repeats away, which a per-file archive cannot. Blocks decode independently, so extraction runs them in parallel.
package noderuntimepack

import (
	"archive/tar"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/bits"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/MichaelKinsy/PiG/internal/procpolicy"
)

// ErrTruncated reports an archive whose block lengths run past its end.
var ErrTruncated = errors.New("noderuntimepack: truncated archive")

// File is one runtime file; Name is a slash-separated path.
type File struct {
	Name string
	Data []byte
}

// blockTarget is the raw size at which a block ends at the next group boundary. A group (one package directory) is never split, so a block holds every copy of the modules it was built from.
const blockTarget = 4 << 20

// Pack returns the blocks for files, in an order that keeps each package directory's files adjacent (a directory walk does). The output depends only on the names and contents.
func Pack(files []File) ([]byte, error) {
	var out []byte
	var block []File
	size := 0
	flush := func() error {
		if len(block) == 0 {
			return nil
		}
		frame, err := compressBlock(block)
		if err != nil {
			return err
		}
		out = binary.AppendUvarint(out, uint64(len(frame)))
		out = append(out, frame...)
		block, size = nil, 0
		return nil
	}
	for i, file := range files {
		if size >= blockTarget && groupKey(file.Name) != groupKey(files[i-1].Name) {
			if err := flush(); err != nil {
				return nil, err
			}
		}
		block = append(block, file)
		size += len(file.Data)
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return out, nil
}

// groupKey names the package directory a file belongs to: shims/pi-dist/<package>, shims/<package>, or the runtime root.
func groupKey(name string) string {
	parts := strings.Split(name, "/")
	switch {
	case len(parts) > 4 && parts[1] == "shims" && parts[2] == "pi-dist":
		return strings.Join(parts[:4], "/")
	case len(parts) > 3 && parts[1] == "shims":
		return strings.Join(parts[:3], "/")
	case len(parts) > 2:
		return strings.Join(parts[:2], "/")
	}
	return parts[0]
}

func compressBlock(files []File) ([]byte, error) {
	var raw bytes.Buffer
	writer := tar.NewWriter(&raw)
	for _, file := range files {
		header := &tar.Header{Name: file.Name, Mode: 0o644, Size: int64(len(file.Data)), ModTime: time.Unix(0, 0), Format: tar.FormatPAX}
		if err := writer.WriteHeader(header); err != nil {
			return nil, err
		}
		if _, err := writer.Write(file.Data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	// The window covers the whole block, so a repeat is found anywhere in it.
	window := max(zstd.MinWindowSize, 1<<bits.Len(uint(raw.Len())))
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedBestCompression), zstd.WithWindowSize(window), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return nil, err
	}
	frame := encoder.EncodeAll(raw.Bytes(), nil)
	return frame, encoder.Close()
}

// Extract decodes every block of archive and calls visit for each file. Blocks run in parallel, so visit must be safe for concurrent use; data is valid only during the call. The first error stops further blocks.
func Extract(archive string, visit func(name string, data []byte) error) error {
	var blocks []string
	for rest := archive; len(rest) > 0; {
		length, n := uvarint(rest)
		if n <= 0 || uint64(len(rest)-n) < length {
			return ErrTruncated
		}
		blocks = append(blocks, rest[n:n+int(length)])
		rest = rest[n+int(length):]
	}
	if len(blocks) > 1 {
		// pig additive (D174): the first start of a Node extension waits on this decode; it runs at the runtime default width.
		defer procpolicy.Widen()()
	}
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		failed error
		jobs   = make(chan string)
	)
	for range min(runtime.GOMAXPROCS(0), len(blocks)) {
		wg.Go(func() {
			for block := range jobs {
				if err := extractBlock(block, visit); err != nil {
					mu.Lock()
					failed = errors.Join(failed, err)
					mu.Unlock()
				}
			}
		})
	}
	for _, block := range blocks {
		mu.Lock()
		stop := failed != nil
		mu.Unlock()
		if stop {
			break
		}
		jobs <- block
	}
	close(jobs)
	wg.Wait()
	return failed
}

func extractBlock(block string, visit func(name string, data []byte) error) error {
	decoder, err := zstd.NewReader(strings.NewReader(block), zstd.WithDecoderConcurrency(1))
	if err != nil {
		return err
	}
	defer decoder.Close()
	reader := tar.NewReader(decoder)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if !fs.ValidPath(header.Name) {
			return fmt.Errorf("noderuntimepack: invalid file name %q", header.Name)
		}
		data := make([]byte, header.Size)
		if _, err := io.ReadFull(reader, data); err != nil {
			return err
		}
		if err := visit(header.Name, data); err != nil {
			return err
		}
	}
}

func uvarint(s string) (uint64, int) {
	return binary.Uvarint([]byte(s[:min(len(s), binary.MaxVarintLen64)]))
}
