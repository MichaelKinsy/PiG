package cellpack

import (
	"fmt"
	"io"
	"math/rand/v2"
	"testing"
	"testing/fstest"
)

// nodeBenchEntries is a member of files files of about size bytes each, spread over folders, with a node_modules link: the
// shape of a Node cell the native builder embeds, small or large.
func nodeBenchEntries(files, size int) []archiveEntry {
	random := rand.New(rand.NewPCG(1, 2))
	entries := []archiveEntry{{name: "ext/", dir: true}, {name: "ext/index.ts", body: "export default function () {}\n"}}
	for folder := 0; folder < max(1, files/50); folder++ {
		entries = append(entries, archiveEntry{name: fmt.Sprintf("ext/node_modules/p%d/", folder), dir: true})
		for file := 0; file < min(files, 50); file++ {
			body := make([]byte, size)
			for i := range body {
				body[i] = byte(random.UintN(256))
			}
			entries = append(entries, archiveEntry{name: fmt.Sprintf("ext/node_modules/p%d/f%d.js", folder, file), body: string(body)})
		}
	}
	return entries
}

// BenchmarkNodeCellWarmStart is the Node cell's part of a Binary's later start, through extract as a start runs it: the archive's
// digest, then the published entry found ready and, since D18, compared with the archive, read where it lies. small is a cell of a few files, large one of 2,000 files and 20 MB.
func BenchmarkNodeCellWarmStart(b *testing.B) {
	for name, entries := range map[string][]archiveEntry{
		"small": nodeBenchEntries(4, 2<<10),
		"large": nodeBenchEntries(2000, 10<<10),
	} {
		b.Run(name, func(b *testing.B) {
			data, digest := nodeArchive(b, entries...)
			m := nodeManifest(digest)
			dest := b.TempDir()
			fsys := fstest.MapFS{"cells/" + m.Cells[0].Binary: {Data: data}}
			if err := extract(fsys, m, dest, io.Discard); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			b.ResetTimer()
			for b.Loop() {
				if err := extract(fsys, m, dest, io.Discard); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
