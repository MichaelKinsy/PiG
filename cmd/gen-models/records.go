package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/zstdblob"
)

// catalogRecordChunk is how many rows' records one compressed chunk holds. It matches ai.catalogRecordChunk.
const catalogRecordChunk = 64

// catalogRecordRef is the reference a row holds for its record: NUL, then the row index, big endian in two bytes. A record is JSON, so no record starts with NUL, and ai.ResolveCatalogRecord tells the two apart by that byte.
func catalogRecordRef(row int) string {
	return string([]byte{0, byte(row >> 8), byte(row)})
}

// writeCatalogRecordChunks stores the rows' records, one per line (compact JSON holds no newline), as one zstd frame per catalogRecordChunk rows in dir/NN.zst. A frame that already decodes to the chunk is kept, so an encoder upgrade does not rewrite committed files.
func writeCatalogRecordChunks(dir string, rows []modelRow) error {
	if len(rows) > 1<<16 {
		return fmt.Errorf("catalog records: %d rows do not fit a two-byte reference", len(rows))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	chunks := (len(rows) + catalogRecordChunk - 1) / catalogRecordChunk
	for c := 0; c < chunks; c++ {
		var b strings.Builder
		for i := c * catalogRecordChunk; i < min(len(rows), (c+1)*catalogRecordChunk); i++ {
			if strings.Contains(rows[i].Record, "\n") {
				return fmt.Errorf("catalog records: row %d's record holds a newline", i)
			}
			b.WriteString(rows[i].Record)
			b.WriteByte('\n')
		}
		path := filepath.Join(dir, fmt.Sprintf("%02d.zst", c))
		existing, _ := os.ReadFile(path)
		if err := os.WriteFile(path, zstdblob.Reuse(existing, []byte(b.String())), 0o644); err != nil {
			return err
		}
	}
	stale, _ := filepath.Glob(filepath.Join(dir, "*.zst"))
	for _, path := range stale {
		var n int
		if _, err := fmt.Sscanf(filepath.Base(path), "%02d.zst", &n); err == nil && n >= chunks {
			if err := os.Remove(path); err != nil {
				return err
			}
		}
	}
	return nil
}
