// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package counts

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"runtime"

	"github.com/MichaelKinsy/PiG/durable/core"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
)

// ReplayError is a replay that stopped because the core did not do what the recorded run did.
type ReplayError struct {
	Build string
	Entry int
	Kind  abi.EventKind
	What  string
}

func (e *ReplayError) Error() string {
	return fmt.Sprintf("counts: %s replay diverged at event %d (kind %d): %s", e.Build, e.Entry, e.Kind, e.What)
}

// intervalKeys bounds the keys one native interval sets: the storage counts and the native build's own.
const intervalKeys = 32

// storage derives the host-side counts of one entry from the recording and the step the core returned.
func storage(e *Entry, step *abi.Step, stepBytes int, m Metrics) {
	m["storage.events"]++
	m["storage.crossings"] += int64(1 + len(e.Reads) + len(e.UUIDs))
	m["storage.reads"] += int64(len(e.Reads))
	for _, r := range e.Reads {
		m["storage.rowsRead"] += int64(r.Rows)
		m["storage.readBytes"] += int64(len(r.Answer))
	}
	m["storage.inBytes"] += int64(len(e.Wire))
	m["storage.outBytes"] += int64(stepBytes)
	m["storage.commits"] += int64(len(step.Commits))
	for _, c := range step.Commits {
		m["storage.statements"] += int64(len(c.Stmts))
		for _, s := range c.Stmts {
			for _, p := range s.Params {
				switch p.Kind {
				case abi.ValueInt, abi.ValueFloat:
					m["storage.writeBytes"] += 8
				case abi.ValueText, abi.ValueBlob:
					m["storage.writeBytes"] += int64(len(p.Bytes))
				}
			}
		}
	}
	m["storage.rowsWritten"] += int64(e.RowsChanged)
	m["storage.effects"] += int64(len(step.Effects))
	m["storage.notices"] += int64(len(step.Notices))
}

// ReplayNative drives the native core through the recording and returns the storage counts (which every build shares) and the
// native build's own: bytes allocated and mallocs inside Step, and the live set (the heap after a collection, less what the
// replay held before it started) at the end of each interval. The host side of a step, decoding the event and the read
// answers, happens outside the measured region.
func ReplayNative(rec *Recording) ([]Interval, error) {
	var cur *Entry
	uuid := 0
	failure := ""
	s := core.NewSession(func() string {
		if uuid >= len(cur.UUIDs) {
			// The step goes on with an id of the right length; the replay stops when it returns.
			uuid++
			failure = "the core took a uuid the recorded run did not take"
			return "00000000-0000-7000-8000-000000000000"
		}
		id := cur.UUIDs[uuid]
		uuid++
		return id
	})
	var answers [][][]abi.Value
	var requests [][]byte
	reads := 0
	s.SetReader(func(sql uint16, params []abi.Value) ([][]abi.Value, error) {
		if reads >= len(answers) {
			failure = "the core made a pig.read call the recorded run did not make"
			return nil, errors.New(failure)
		}
		if !bytes.Equal(abi.AppendReadRequest(nil, sql, params), requests[reads]) {
			failure = "the core asked a different pig.read than the recorded run"
			return nil, errors.New(failure)
		}
		rows := answers[reads]
		reads++
		return rows, nil
	})

	// The intervals' metrics exist before the base reading, each map sized for every key an interval sets, so the live set
	// at the end of an interval holds the core's state and not the replay's own bookkeeping (a map filled per interval read
	// as about 1 KB of growth per turn).
	ivs := intervalsOf(rec)
	out := make([]Interval, 0, len(ivs))
	metrics := make([]Metrics, len(ivs))
	for i := range metrics {
		metrics[i] = make(Metrics, intervalKeys)
	}

	runtime.GC()
	var base, before, after runtime.MemStats
	runtime.ReadMemStats(&base)

	for n, iv := range ivs {
		m := metrics[n]
		for i := iv.from; i < iv.to; i++ {
			e := &rec.Entries[i]
			cur, uuid, reads = e, 0, 0
			answers, requests = answers[:0], requests[:0]
			for _, r := range e.Reads {
				rows, err := abi.DecodeAnswer(r.Answer)
				if err != nil {
					return nil, &ReplayError{"native", i, e.Kind, "a recorded read answer does not decode: " + err.Error()}
				}
				answers, requests = append(answers, rows), append(requests, r.Request)
			}
			ev, err := abi.DecodeEvent(e.Kind, e.Now, e.Wire)
			if err != nil {
				return nil, &ReplayError{"native", i, e.Kind, "a recorded event does not decode: " + err.Error()}
			}
			runtime.ReadMemStats(&before)
			step := s.Step(ev)
			runtime.ReadMemStats(&after)
			m["native.allocBytes"] += int64(after.TotalAlloc - before.TotalAlloc)
			m["native.mallocs"] += int64(after.Mallocs - before.Mallocs)
			if failure != "" {
				return nil, &ReplayError{"native", i, e.Kind, failure}
			}
			wire := abi.AppendStep(nil, step)
			if e.Verified && sha256.Sum256(wire) != e.Digest {
				return nil, &ReplayError{"native", i, e.Kind, "the step differs from the recorded step"}
			}
			if reads != len(e.Reads) || uuid != len(e.UUIDs) {
				return nil, &ReplayError{"native", i, e.Kind, fmt.Sprintf("the core made %d reads and took %d uuids, the recorded run %d and %d", reads, uuid, len(e.Reads), len(e.UUIDs))}
			}
			storage(e, step, len(wire), m)
		}
		runtime.GC()
		runtime.ReadMemStats(&after)
		m["native.liveBytes"] = int64(after.HeapAlloc) - int64(base.HeapAlloc)
		m["native.liveObjects"] = int64(after.HeapObjects) - int64(base.HeapObjects)
		out = append(out, Interval{iv.name, m})
	}
	return out, nil
}
