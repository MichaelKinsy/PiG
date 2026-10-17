// SPDX-License-Identifier: MIT

// Command divsplit generates docs/parity/DIVERGENCES.md from the one-file-per-record sources in
// docs/parity/divergences, and checks that the committed ledger equals what the sources produce.
//
// Each divergence is edited in its own file, so lanes that touch different records never conflict. The
// generated ledger stays committed because the divergence gates, the docs bundle and the call-site markers read it.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	headFile        = "head.md"
	activeIntroFile = "active-intro.md"
	retiredHeading  = "## Retired divergences"
	activeHeading   = "## Active divergences"
)

var (
	recordFileRE   = regexp.MustCompile(`^D([1-9][0-9]*)\.md$`)
	activeFirstRE  = regexp.MustCompile(`^## D([1-9][0-9]*) \S`)
	retiredFirstRE = regexp.MustCompile(`^- D([1-9][0-9]*) — \S`)
)

type record struct {
	number  int
	text    string
	retired bool
}

// Generate returns the ledger the record files in dir produce: the head, the retired records and the active records,
// each group in ascending numeric order.
func Generate(dir string) ([]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var head, intro string
	var records []record
	for _, entry := range entries {
		name := entry.Name()
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		text := string(data)
		switch {
		case name == headFile:
			head = text
		case name == activeIntroFile:
			intro = text
		case recordFileRE.MatchString(name):
			number, _ := strconv.Atoi(recordFileRE.FindStringSubmatch(name)[1])
			rec, err := parseRecord(name, number, text)
			if err != nil {
				return nil, err
			}
			records = append(records, rec)
		default:
			return nil, fmt.Errorf("%s: not %s, %s or D<N>.md", filepath.Join(dir, name), headFile, activeIntroFile)
		}
	}
	if head == "" || intro == "" {
		return nil, fmt.Errorf("%s needs %s and %s", dir, headFile, activeIntroFile)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].number < records[j].number })
	var out bytes.Buffer
	out.WriteString(strings.TrimRight(head, "\n"))
	out.WriteString("\n\n" + retiredHeading + "\n\n")
	for _, rec := range records {
		if rec.retired {
			out.WriteString(rec.text + "\n\n")
		}
	}
	out.WriteString(activeHeading + "\n\n")
	out.WriteString(strings.TrimRight(intro, "\n") + "\n")
	for _, rec := range records {
		if !rec.retired {
			out.WriteString("\n" + rec.text + "\n")
		}
	}
	return out.Bytes(), nil
}

// parseRecord checks that a record file holds one record of its own number: an active section that starts with its
// "## D<N> title" heading, or one retired "- D<N> — ..." line.
func parseRecord(name string, number int, text string) (record, error) {
	text = strings.TrimRight(text, "\n")
	first, _, _ := strings.Cut(text, "\n")
	if m := activeFirstRE.FindStringSubmatch(first); m != nil {
		if m[1] != strconv.Itoa(number) {
			return record{}, fmt.Errorf("%s starts with the heading of D%s", name, m[1])
		}
		for line := range strings.SplitSeq(text, "\n") {
			if strings.HasPrefix(line, "## ") && line != first {
				return record{}, fmt.Errorf("%s holds a second level-two heading %q; one file holds one record", name, line)
			}
		}
		return record{number: number, text: text}, nil
	}
	if m := retiredFirstRE.FindStringSubmatch(first); m != nil {
		if m[1] != strconv.Itoa(number) {
			return record{}, fmt.Errorf("%s starts with the entry of D%s", name, m[1])
		}
		if strings.Contains(text, "\n") {
			return record{}, fmt.Errorf("%s: a retired record is one line", name)
		}
		return record{number: number, text: text, retired: true}, nil
	}
	return record{}, fmt.Errorf("%s must start with \"## D%d <title>\" (active) or \"- D%d — <text>\" (retired)", name, number, number)
}

func run(args []string) error {
	dir, out, check := "docs/parity/divergences", "docs/parity/DIVERGENCES.md", false
	for _, arg := range args {
		switch {
		case arg == "-check":
			check = true
		case strings.HasPrefix(arg, "-dir="):
			dir = strings.TrimPrefix(arg, "-dir=")
		case strings.HasPrefix(arg, "-out="):
			out = strings.TrimPrefix(arg, "-out=")
		default:
			return fmt.Errorf("usage: divsplit [-check] [-dir=docs/parity/divergences] [-out=docs/parity/DIVERGENCES.md]")
		}
	}
	generated, err := Generate(dir)
	if err != nil {
		return err
	}
	if check {
		committed, err := os.ReadFile(out)
		if err != nil {
			return err
		}
		if !bytes.Equal(committed, generated) {
			return fmt.Errorf("%s differs from the records in %s; run: make divergences", out, dir)
		}
		return nil
	}
	return os.WriteFile(out, generated, 0o644)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "divsplit:", err)
		if errors.Is(err, os.ErrNotExist) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
