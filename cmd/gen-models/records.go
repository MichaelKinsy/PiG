package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// catalogRecordChunk is how many rows' records one compressed chunk holds. It matches ai.catalogRecordChunk.
const catalogRecordChunk = 64

// catalogRecordRef is the reference a row holds for its record: NUL, then the row index, big endian in two bytes. A record is JSON, so no record starts with NUL, and ai.ResolveCatalogRecord tells the two apart by that byte.
func catalogRecordRef(row int) string {
	return string([]byte{0, byte(row >> 8), byte(row)})
}

// writeCatalogRecordChunks stores the rows' records, one per line (compact JSON holds no newline), as one gzip stream per catalogRecordChunk rows in dir/NN.gz (gzip, not zstd: a build with every strip tag links no zstd decoder, D92). A file that already inflates to the chunk is kept, so a compressor upgrade does not rewrite committed files.
func writeCatalogRecordChunks(dir string, rows []modelRow) error {
	if len(rows) > 1<<16 {
		return fmt.Errorf("catalog records: %d rows do not fit a two-byte reference", len(rows))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	chunks := (len(rows) + catalogRecordChunk - 1) / catalogRecordChunk
	for c := range chunks {
		var b strings.Builder
		for i := c * catalogRecordChunk; i < min(len(rows), (c+1)*catalogRecordChunk); i++ {
			if strings.Contains(rows[i].Record, "\n") {
				return fmt.Errorf("catalog records: row %d's record holds a newline", i)
			}
			b.WriteString(rows[i].Record)
			b.WriteByte('\n')
		}
		path := filepath.Join(dir, fmt.Sprintf("%02d.gz", c))
		existing, _ := os.ReadFile(path)
		if err := os.WriteFile(path, reuseGzip(existing, []byte(b.String())), 0o644); err != nil {
			return err
		}
	}
	stale, _ := filepath.Glob(filepath.Join(dir, "*.gz"))
	for _, path := range stale {
		var n int
		if _, err := fmt.Sscanf(filepath.Base(path), "%02d.gz", &n); err == nil && n >= chunks {
			if err := os.Remove(path); err != nil {
				return err
			}
		}
	}
	return nil
}

// reuseGzip returns existing when it already inflates to raw, and raw compressed at the best level otherwise.
func reuseGzip(existing, raw []byte) []byte {
	if reader, err := gzip.NewReader(bytes.NewReader(existing)); err == nil {
		if got, err := io.ReadAll(reader); err == nil && bytes.Equal(got, raw) {
			return existing
		}
	}
	var out bytes.Buffer
	writer, _ := gzip.NewWriterLevel(&out, gzip.BestCompression)
	_, _ = writer.Write(raw)
	_ = writer.Close()
	return out.Bytes()
}
