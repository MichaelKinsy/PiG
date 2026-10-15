// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Options controls one report build.
type Options struct {
	// Root is the repository root.
	Root string
	// Out is the output directory.
	Out string
	// TotalsLog is an optional ledger totals log for the trend line.
	TotalsLog string
}

// Report is everything the output is rendered from.
type Report struct {
	Opts        Options
	Pin         Pin
	Head        string
	HeadDate    string
	Ledgers     []*PinLedger
	Overview    Overview
	Divergences []*Divergence
	Bugs        *BugLedger
	Evals       Evals
	PiMirror    string
	PiAvailable bool
	Syms        *symIndex
	Families    []family
}

// ledgerID is the file-safe id of a pin ledger.
func ledgerID(version string) string { return strings.NewReplacer("+", "_", ".", "-").Replace(version) }

// Build loads every data source of the repository.
func Build(opts Options) (*Report, error) {
	root, err := findRoot(opts.Root)
	if err != nil {
		return nil, err
	}
	opts.Root = root
	pin, err := readPin(root)
	if err != nil {
		return nil, err
	}
	rep := &Report{Opts: opts, Pin: pin}
	rep.Head, _ = gitOut(root, "rev-parse", "--short=10", "HEAD")
	rep.HeadDate, _ = gitOut(root, "log", "-1", "--format=%cs")
	rep.Syms = newSymIndex(root)
	rep.Families = loadFamilies(root)

	piRoot := filepath.Join(root, ".upstream", "v"+pin.Version)
	pi := newPiSource(piRoot)
	rep.PiMirror, rep.PiAvailable = piRoot, pi.available()

	targets := pinTargets(pin.Version, ledgerVersions(root))
	if len(targets) == 0 {
		return nil, fmt.Errorf("no mapping ledger for pin %s under test/parity/interfaces", pin.Version)
	}
	for _, v := range targets {
		pl, err := loadPinLedger(root, v, v == pin.Version, rep.Syms, rep.Families)
		if err != nil {
			return nil, fmt.Errorf("ledger %s: %w", v, err)
		}
		rep.Ledgers = append(rep.Ledgers, pl)
	}

	rep.Bugs, err = loadBugs(root)
	if err != nil {
		return nil, err
	}
	scan, err := scanTree(root)
	if err != nil {
		return nil, err
	}
	rep.Divergences, err = loadDivergences(root, rep.Bugs, scan, rep.Syms, pi, pin.Version)
	if err != nil {
		return nil, err
	}
	rep.Evals = loadEvals(root)
	rep.Overview = buildOverview(rep, opts)
	return rep, nil
}

func buildOverview(rep *Report, opts Options) Overview {
	ov := Overview{Pin: rep.Pin, Head: rep.Head, HeadDate: rep.HeadDate, PiMirror: rep.PiAvailable}
	var follow *PinLedger
	for _, pl := range rep.Ledgers {
		role := "pinned"
		if !pl.Committed {
			role = "main-follow target"
			follow = pl
		}
		ov.Ledgers = append(ov.Ledgers, summarize(pl, role))
	}
	ov.Ratchets = loadRatchets(rep.Opts.Root, rep.Pin.Version)
	if opts.TotalsLog != "" {
		if t := parseTotalsLog(opts.TotalsLog); t != nil {
			ov.Trends = append(ov.Trends, *t)
		}
	}
	for _, pl := range rep.Ledgers {
		rel := "test/parity/interface-closure/gaps/interface-gaps-v" + pl.Version + ".tsv"
		if t := gitGapTrend(rep.Opts.Root, rel, "Gap baseline rows, Pi "+pl.Version); t != nil {
			ov.Trends = append(ov.Trends, *t)
		}
	}
	if follow != nil {
		ov.FlipLabel = follow.Version
		s := summarize(follow, "")
		ov.Flip = append(ov.Flip, FlipItem{Name: "Interface rows closed (not a gap)", Done: s.Total - s.Gap, Pending: s.Gap, Source: "test/parity/interfaces/mapping-v" + follow.Version + ".json"})
		ov.Flip = append(ov.Flip, loadFlip(rep.Opts.Root, follow.Version)...)
	}
	return ov
}
