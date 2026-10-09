package piglet

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/piglet/artifact"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// pig additive (D92): the strip delta report. When a Binary record exists for
// a Piglet, every resolution against the running core (`pig piglet build`,
// `pig piglet show`) states the built-in IDs this core's strip table adds
// over the table the newest Binary was built against: the ones a keep-mode
// list leaves out and the ones a deny-mode list now includes.

// StripDelta is the change of the strip table since a Piglet's newest Binary.
type StripDelta struct {
	// FromPigVersion is the version of the core that built the Binary.
	FromPigVersion string
	// ToPigVersion is the running core's version.
	ToPigVersion string
	// Added counts the IDs the running core's table adds.
	Added int
	// LeftOut names the added IDs a keep-mode list strips, as slot IDs.
	LeftOut []string
	// NowIncluded names the added IDs a deny-mode list leaves enabled.
	NowIncluded []string
}

// Lines renders the report: one header line, then the two lists when the
// table adds an ID.
func (d StripDelta) Lines() []string {
	if d.Added == 0 {
		return []string{fmt.Sprintf("Strip table: PiG %s → %s adds no built-in IDs", d.FromPigVersion, d.ToPigVersion)}
	}
	noun := "IDs"
	if d.Added == 1 {
		noun = "ID"
	}
	list := func(ids []string) string {
		if len(ids) == 0 {
			return "none"
		}
		return strings.Join(ids, ", ")
	}
	return []string{
		fmt.Sprintf("Strip table: PiG %s → %s adds %d built-in %s", d.FromPigVersion, d.ToPigVersion, d.Added, noun),
		fmt.Sprintf("  %-26s%s", "left out (keep mode):", list(d.LeftOut)),
		fmt.Sprintf("  %-26s%s", "now included (deny mode):", list(d.NowIncluded)),
	}
}

// stripDeltaSince compares the running core's strip table with the table of
// the previous Binary record. current is the Piglet's strip spec resolved by
// this core; with nil, the record's keep-mode lists give each list's mode.
// ok is false when the record has no strip table.
func stripDeltaSince(previous *artifact.Binary, current *StripSpec) (delta StripDelta, ok bool) {
	if previous == nil || len(previous.StripTable) == 0 {
		return StripDelta{}, false
	}
	delta = StripDelta{FromPigVersion: previous.PigVersion, ToPigVersion: pigversion.PigVersion}
	recorded := func(lists []artifact.StripIDs, kind string) ([]string, bool) {
		for _, list := range lists {
			if list.Kind == kind {
				return list.IDs, true
			}
		}
		return nil, false
	}
	for _, list := range current.lists() {
		table, _ := recorded(previous.StripTable, list.kind)
		keepMode := list.keep != nil
		if current == nil {
			_, keepMode = recorded(previous.StripKeep, list.kind)
		}
		for _, id := range list.known {
			if slices.Contains(table, id) {
				continue
			}
			delta.Added++
			slot := StrippedID{Kind: list.kind, ID: id}.SlotID()
			switch {
			case keepMode && (current == nil || slices.Contains(list.ids, id)):
				delta.LeftOut = append(delta.LeftOut, slot)
			case !keepMode && !slices.Contains(list.ids, id):
				delta.NowIncluded = append(delta.NowIncluded, slot)
			}
		}
	}
	return delta, true
}

// newestBinaryRecord returns the newest Binary record PiG built for the
// Piglet, or nil when there is none. A record that does not parse is skipped:
// the report is advice, and `pig piglet list` reports a damaged store.
func newestBinaryRecord(name string) *artifact.Record {
	if name == "" || strings.ContainsAny(name, `/\`) {
		return nil
	}
	var newest *artifact.Record
	_ = filepath.WalkDir(filepath.Join(codingagent.PigletRecordsDir(), name), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(path) != ".json" || !entry.Type().IsRegular() {
			return nil
		}
		record, err := readRecordFile(path)
		if err != nil || record.Kind != artifact.RecordKindBinary || record.Piglet != name || record.Binary == nil {
			return nil
		}
		if newest == nil || cmp.Or(cmp.Compare(record.CreatedAt, newest.CreatedAt), cmp.Compare(record.Digest, newest.Digest)) > 0 {
			newest = &record
		}
		return nil
	})
	return newest
}

// StripDeltaReport returns the strip delta report for the Piglet against its
// newest Binary record, with current as the Piglet's strip spec resolved now
// (nil reads the modes from the record). It returns nil when PiG has built no
// Binary for the Piglet.
func StripDeltaReport(name string, current *StripSpec) []string {
	delta, ok := stripDeltaSince(binaryPayload(newestBinaryRecord(name)), current)
	if !ok {
		return nil
	}
	return delta.Lines()
}

func binaryPayload(record *artifact.Record) *artifact.Binary {
	if record == nil {
		return nil
	}
	return record.Binary
}
