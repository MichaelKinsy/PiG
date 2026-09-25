package codingagent

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
	settingsClosureTracePath    = flag.String("settings-closure-trace", "", "write a typed settings closure trace")
	settingsClosureTraceTargets = flag.String("settings-closure-trace-targets", "", "comma-separated closure target IDs")
	settingsClosureTraceType    = flag.String("settings-closure-trace-type", "persistence-roundtrip", "settings closure witness type")
)

type settingsClosureTraceEvent struct {
	kind    string
	subject string
	value   string
}

func writeSettingsClosureTrace(t *testing.T, events ...settingsClosureTraceEvent) {
	t.Helper()
	if *settingsClosureTracePath == "" {
		return
	}
	if *settingsClosureTraceTargets == "" || len(events) == 0 {
		t.Fatal("settings closure trace requires targets and events")
	}
	targets := strings.Split(*settingsClosureTraceTargets, ",")
	if !slices.IsSorted(targets) || len(slices.Compact(slices.Clone(targets))) != len(targets) {
		t.Fatal("settings closure trace targets must be sorted and unique")
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
	}{WitnessType: *settingsClosureTraceType, Captures: make([]traceCapture, len(targets))}
	for targetIndex, target := range targets {
		settingID := target[strings.LastIndexByte(target, ':')+1:]
		var selected []settingsClosureTraceEvent
		for _, event := range events {
			if event.subject == settingID || event.subject == "display-settings" {
				selected = append(selected, event)
			}
		}
		capture := traceCapture{TargetID: target, Events: make([]traceEvent, len(selected))}
		for eventIndex, event := range selected {
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
	if err := os.WriteFile(*settingsClosureTracePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
