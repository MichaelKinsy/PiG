// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"slices"

	"github.com/BurntSushi/toml"
)

const acknowledgedPath = "automation/ci/preflight/acknowledged.toml"

type acknowledgement struct {
	Check string `toml:"check"`
	File  string `toml:"file"`
	Blob  string `toml:"blob"`
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
		if a.Check == "" || a.File == "" || a.Blob == "" {
			return nil, fmt.Errorf("%s: entry %d needs check, file and blob", acknowledgedPath, i+1)
		}
	}
	return doc.Ack, nil
}

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
				if a.Check == name && a.File == w.File && a.Blob == w.Blob {
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
		stale := warning{File: a.File, Blob: blob, Text: fmt.Sprintf("has an entry in %s for blob %s, but the file changed: check it again and update or delete the entry", acknowledgedPath, short(a.Blob))}
		// First, so the cap on listed files never hides it.
		if !slices.Contains(v.Warnings, stale) {
			v.Warnings = append([]warning{stale}, v.Warnings...)
		}
		ev.Checks[a.Check] = v
	}
}

func ackEntry(check string, w warning) string {
	return fmt.Sprintf("[[ack]]\ncheck = %q\nfile = %q\nblob = %q\n", check, w.File, w.Blob)
}
