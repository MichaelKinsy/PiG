package codingagent

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// BenchmarkSessionScaleReads times the whole-history reads of a resumed session file (SESSIONSCALE_FIXTURE, for example the bench/sessionscale
// owner-shape fixture's session.jsonl) against Pi's SessionManager on the same file: the public decoded reads, the lazy reads the serve paths
// use, and the serve paths themselves. Run it with -cpu 1,8 -benchmem.
func BenchmarkSessionScaleReads(b *testing.B) {
	fixture := os.Getenv("SESSIONSCALE_FIXTURE")
	if fixture == "" {
		b.Skip("set SESSIONSCALE_FIXTURE to a session file")
	}
	path := filepath.Join(b.TempDir(), "s.jsonl")
	if err := copyFile(fixture, path); err != nil {
		b.Fatal(err)
	}
	session, _, err := loadPersistedSession(path)
	if err != nil {
		b.Fatal(err)
	}
	leaf := *session.GetLeafID()
	reads := HandleReads{Session: session}
	rows := []struct {
		name string
		read func() error
	}{
		{"GetEntries", func() error { session.GetEntries(); return nil }},
		{"GetBranch", func() error { session.GetBranch(); return nil }},
		{"GetTree", func() error { session.GetTree(); return nil }},
		{"GetLabel", func() error { session.GetLabel(leaf); return nil }},
		{"lazy/GetEntries", func() error { reads.GetEntries(); return nil }},
		{"lazy/GetBranch", func() error { reads.GetBranch(); return nil }},
		{"lazy/GetTree", func() error { reads.GetTree(); return nil }},
		{"rpc/get_entries", func() error {
			records, err := sessionentry.ReadRaws(reads.GetEntries())
			if err != nil {
				return err
			}
			_, err = json.Marshal(map[string]any{"entries": records, "leafId": leaf})
			return err
		}},
		{"extension/getEntries", func() error { _, err := newSessionReadLog(session); return err }},
	}
	for _, row := range rows {
		b.Run(row.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := row.read(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
