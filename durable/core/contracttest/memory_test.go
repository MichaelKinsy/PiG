package contracttest

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"
)

func TestMemoryLogWritesOneSamplePerTurn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mem.jsonl")
	log, err := OpenMemoryLog(path)
	if err != nil {
		t.Fatal(err)
	}
	for turn := range 3 {
		log.Sample(turn)
	}
	log.Close()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var lines []string
	for sc := bufio.NewScanner(f); sc.Scan(); {
		lines = append(lines, sc.Text())
	}
	if len(lines) != 3 || lines[2][:10] != `{"turn":2,` {
		t.Fatalf("samples = %q", lines)
	}
	if residentBytes() == 0 {
		t.Fatal("resident set is zero")
	}
}

func TestNilMemoryLogRecordsNothing(t *testing.T) {
	log, err := OpenMemoryLog("")
	if err != nil || log != nil {
		t.Fatalf("OpenMemoryLog(\"\") = %v, %v", log, err)
	}
	log.Sample(0)
	log.Close()
}
