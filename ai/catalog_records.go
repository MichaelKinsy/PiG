package ai

import (
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"
)

// A built-in catalog row's Record is a three-byte reference (NUL, then the row index in two bytes, big endian), not the 600 bytes of JSON it stands for: the records of all 1,629 rows were 0.9 MB of the binary and compress to 55 KB. cmd/gen-models writes them to catalog_records/NN.gz, one gzip stream per catalogRecordChunk rows, one record per line. ResolveCatalogRecord returns the JSON and expands a chunk on its first use, so a start that reads no record expands none and a start that reads one expands 64.
//
// Every read of a record's content (GeneratedModel.Record, Model.catalogRecord, codingagent.ModelEntry.CatalogRecord) goes through ResolveCatalogRecord. Comparisons of two references and tests for "" need not.

//go:embed catalog_records/*.gz
var catalogRecordFiles embed.FS

// catalogRecordChunk is the number of rows in one chunk; cmd/gen-models writes the chunks with the same constant.
const catalogRecordChunk = 64

type recordChunk struct {
	once  sync.Once
	lines []string
}

// expandedCatalogChunks counts the chunks expanded so far; CatalogChunksExpanded exposes it so tests can show that a start path reads no record.
var expandedCatalogChunks atomic.Int32

// CatalogChunksExpanded is the number of built-in catalog record chunks expanded (decompressed) in this process so far.
func CatalogChunksExpanded() int { return int(expandedCatalogChunks.Load()) }

var recordChunks [(len(generatedRows) + catalogRecordChunk - 1) / catalogRecordChunk]recordChunk

// isCatalogRecordRef reports whether s is a built-in row's own reference. A record is a JSON object, so it never starts with NUL.
func isCatalogRecordRef(s string) bool { return len(s) == 3 && s[0] == 0 }

// deriveCatalogRecord is rewriteRecordIdentity(record, id, name) for a record that may be a reference. The reference of a built-in row becomes a longer reference (NUL, row, NUL, id, NUL, name) that ResolveCatalogRecord rewrites when its JSON is read, so listing the OpenRouter routing variants of the catalog expands no chunk.
func deriveCatalogRecord(record, id, name string) string {
	if isCatalogRecordRef(record) {
		return record + "\x00" + id + "\x00" + name
	}
	return rewriteRecordIdentity(ResolveCatalogRecord(record), id, name)
}

// ResolveCatalogRecord returns the JSON that record stands for: the built-in row it refers to, that row's record with an id and a name rewritten (deriveCatalogRecord), or record itself when it is JSON already (a models store record, an extension's, or "").
func ResolveCatalogRecord(record string) string {
	if len(record) < 3 || record[0] != 0 {
		return record
	}
	row := int(record[1])<<8 | int(record[2])
	chunk := &recordChunks[row/catalogRecordChunk]
	chunk.once.Do(func() {
		name := fmt.Sprintf("catalog_records/%02d.gz", row/catalogRecordChunk)
		frame, err := catalogRecordFiles.ReadFile(name)
		if err != nil {
			panic("ai: " + err.Error())
		}
		raw, err := inflateCatalogChunk(frame)
		if err != nil {
			panic("ai: " + name + ": " + err.Error())
		}
		// raw is never written again, so the lines may alias it: a copy of each 38 KB chunk would double what a start that reads every record allocates.
		chunk.lines = strings.Split(unsafe.String(unsafe.SliceData(raw), len(raw)), "\n") //nolint:gosec // G103: raw is the chunk's own buffer, read-only from here on and kept alive by the strings cut from it.
		expandedCatalogChunks.Add(1)
	})
	base := chunk.lines[row%catalogRecordChunk]
	if len(record) == 3 {
		return base
	}
	id, name, _ := strings.Cut(record[4:], "\x00")
	return rewriteRecordIdentity(base, id, name)
}

// inflateCatalogChunk reads the gzip stream of a chunk into one buffer sized from the stream's trailer and verifies the checksum at its end. The records are gzip, not zstd, so a build that strips every zstd frame (D92) still links no zstd decoder, and a chunk needs no decoder buffers beyond the 40 KB flate reader.
func inflateCatalogChunk(data []byte) ([]byte, error) {
	if len(data) < 4 {
		return nil, io.ErrUnexpectedEOF
	}
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	raw := make([]byte, binary.LittleEndian.Uint32(data[len(data)-4:]))
	if _, err := io.ReadFull(reader, raw); err != nil {
		return nil, err
	}
	var extra [1]byte
	if count, err := reader.Read(extra[:]); count != 0 || !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("the stream does not end at the trailer's size %d: %v", len(raw), err)
	}
	return raw, nil
}
