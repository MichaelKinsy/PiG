package compaction

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"slices"
	"strings"
	"testing"
)

var (
	closureTracePath    = flag.String("closure-trace", "", "write a typed closure trace")
	closureTraceType    = flag.String("closure-trace-type", "", "typed closure trace witness type")
	closureTraceTargets = flag.String("closure-trace-targets", "", "comma-separated closure target IDs")
)

type closureTraceEvent struct {
	kind    string
	subject string
	value   string
}

func writeClosureTrace(t *testing.T, events ...closureTraceEvent) {
	t.Helper()
	if *closureTracePath == "" {
		return
	}
	if *closureTraceType == "" || *closureTraceTargets == "" || len(events) == 0 {
		t.Fatal("closure trace requires type, targets, and events")
	}
	targets := strings.Split(*closureTraceTargets, ",")
	if !slices.IsSorted(targets) || len(slices.Compact(slices.Clone(targets))) != len(targets) {
		t.Fatal("closure trace targets must be sorted and unique")
	}
	type traceEvent struct {
		Ordinal   int    `json:"ordinal"`
		Kind      string `json:"kind"`
		Subject   string `json:"subject"`
		ValueHash string `json:"valueHash"`
	}
	type traceCapture struct {
		TargetID string       `json:"targetId"`
		Events   []traceEvent `json:"events"`
	}
	artifact := struct {
		WitnessType string         `json:"witnessType"`
		Captures    []traceCapture `json:"captures"`
	}{WitnessType: *closureTraceType, Captures: make([]traceCapture, len(targets))}
	for targetIndex, target := range targets {
		capture := traceCapture{TargetID: target, Events: make([]traceEvent, len(events))}
		for eventIndex, event := range events {
			sum := sha256.Sum256([]byte(event.value))
			capture.Events[eventIndex] = traceEvent{
				Ordinal: eventIndex + 1, Kind: event.kind, Subject: event.subject,
				ValueHash: "sha256:" + hex.EncodeToString(sum[:]),
			}
		}
		artifact.Captures[targetIndex] = capture
	}
	data, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(*closureTracePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
