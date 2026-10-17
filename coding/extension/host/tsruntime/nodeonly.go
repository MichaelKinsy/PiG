package tsruntime

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// NodeOnlyDir is the directory inside the slot root where PiG records the extension sets that only Node runs.
const NodeOnlyDir = ".node-only"

// nodeOnlyTTL bounds how long a record keeps a set off the requested runtime: a PiG or runtime upgrade may fix what failed.
// pig additive (D19): Pi has one runtime, so no set is kept off another.
const nodeOnlyTTL = 7 * 24 * time.Hour

// nodeOnlyRecord is one extension set that failed to start on a slot runtime and started on Node.
type nodeOnlyRecord struct {
	Members []Member `json:"members"`
	Detail  string   `json:"detail"`
	// RecordedAt is the Unix time in seconds.
	RecordedAt int64 `json:"recordedAt"`
	// Inputs are each member's entry and the package.json beside or above it. A change to one retries the set on the requested runtime.
	Inputs []Input `json:"inputs"`
}

func nodeOnlyPath(root string, set Set) string {
	return filepath.Join(root, NodeOnlyDir, set.Key+".json")
}

func (r nodeOnlyRecord) current() bool {
	return len(r.Inputs) != 0 && staleInput(r.Inputs) == "" && time.Since(time.Unix(r.RecordedAt, 0)) <= nodeOnlyTTL
}

// RecordNodeOnly remembers that set started on Node after a slot runtime failed to start it, so later launches of the set run on Node.
// pig additive (D19): Pi has one runtime, so it has no runtime to fall back from.
func RecordNodeOnly(root string, set Set, detail string) error {
	if root == "" || !hexPattern.MatchString(set.Key) {
		return errors.New("the runtime slot root or the set key is unknown")
	}
	record := nodeOnlyRecord{Members: set.Members, Detail: detail, RecordedAt: time.Now().Unix()}
	for _, member := range set.Members {
		for _, path := range []string{member.Entry, packageJSONFor(member.Entry)} {
			if path == "" {
				continue
			}
			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			record.Inputs = append(record.Inputs, Input{Path: path, Size: info.Size(), MtimeNs: info.ModTime().UnixNano()})
		}
	}
	//portlint:allow jsonescape PiG-only format with no Pi counterpart (Pi has no Piglets or runtime slot)
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(nodeOnlyPath(root, set), append(data, '\n'))
}

// ReadNodeOnly returns the detail recorded for set when none of the files it was recorded from changed since.
func ReadNodeOnly(root string, set Set) (string, bool) {
	if root == "" || !hexPattern.MatchString(set.Key) {
		return "", false
	}
	path := nodeOnlyPath(root, set)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var record nodeOnlyRecord
	if json.Unmarshal(data, &record) != nil || !record.current() {
		_ = os.Remove(path)
		return "", false
	}
	return record.Detail, true
}

// NodeOnlyEntry is one recorded Node-only extension set, for status output.
type NodeOnlyEntry struct {
	Members []Member `json:"members"`
	Detail  string   `json:"detail"`
}

// ListNodeOnly returns the current Node-only records in file name order.
func ListNodeOnly(root string) []NodeOnlyEntry {
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(root, NodeOnlyDir))
	if err != nil {
		return nil
	}
	var listed []NodeOnlyEntry
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(root, NodeOnlyDir, entry.Name()))
		if err != nil {
			continue
		}
		var record nodeOnlyRecord
		if json.Unmarshal(data, &record) == nil && record.current() {
			listed = append(listed, NodeOnlyEntry{Members: record.Members, Detail: record.Detail})
		}
	}
	return listed
}

// packageJSONFor returns the nearest package.json at or above entry's directory, or "".
func packageJSONFor(entry string) string {
	dir := filepath.Dir(entry)
	for range 16 {
		candidate := filepath.Join(dir, "package.json")
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}
