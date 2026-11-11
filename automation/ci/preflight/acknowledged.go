// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
)

// acknowledgedPath holds the warnings a reviewer has seen. An entry names the check, the file, the exact notice and the
// file's git blob at the time: a later change to the file changes its blob, so the warning shows again.
const acknowledgedPath = "automation/ci/preflight/acknowledged.toml"

type acknowledgement struct {
	Check  string `toml:"check"`
	File   string `toml:"file"`
	Notice string `toml:"notice"`
	Blob   string `toml:"blob"`
}

func parseAcknowledged(text string) ([]acknowledgement, error) {
	var doc struct {
		Ack []acknowledgement `toml:"ack"`
	}
	md, err := toml.Decode(text, &doc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", acknowledgedPath, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		return nil, fmt.Errorf("%s: unknown key %s", acknowledgedPath, undecoded[0])
	}
	for i, a := range doc.Ack {
		if a.Check == "" || a.File == "" || a.Notice == "" || a.Blob == "" {
			return nil, fmt.Errorf("%s: entry %d needs check, file, notice and blob", acknowledgedPath, i+1)
		}
	}
	return doc.Ack, nil
}

// applyAcknowledged marks the warnings an entry matches and returns a warning for each entry of a changed file whose
// blob no longer matches, so stale entries are cleaned up.
func applyAcknowledged(ev *evaluation, d *data, acks []acknowledgement) {
	blobs := map[string]string{}
	for _, f := range d.Files {
		blobs[f.Filename] = f.SHA
	}
	used := make([]bool, len(acks))
	for name, v := range ev.Checks {
		for i := range v.Warnings {
			w := &v.Warnings[i]
			for j, a := range acks {
				if a.Check == name && a.File == w.File && a.Notice == w.Notice && a.Blob == w.Blob {
					w.Acknowledged = true
					used[j] = true
				}
			}
		}
		ev.Checks[name] = v
	}
	for j, a := range acks {
		blob, changed := blobs[a.File]
		if used[j] || !changed || blob == a.Blob {
			continue
		}
		v, ok := ev.Checks[a.Check]
		if !ok {
			continue
		}
		v.Warnings = append(v.Warnings, warning{File: a.File, Blob: blob, Notice: a.Notice, Text: fmt.Sprintf("has an entry in %s for blob %s, but the file changed: check the notice again and update or delete the entry", acknowledgedPath, short(a.Blob))})
		ev.Checks[a.Check] = v
	}
}

// ackEntry is the TOML entry that acknowledges a warning, printed for the reviewer to paste.
func ackEntry(check string, w warning) string {
	q := func(s string) string { return `'''` + strings.ReplaceAll(s, `'''`, `''""`) + `'''` }
	return fmt.Sprintf("[[ack]]\ncheck = %q\nfile = %q\nnotice = %s\nblob = %q\n", check, w.File, q(w.Notice), w.Blob)
}
