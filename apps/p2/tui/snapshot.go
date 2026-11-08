package tui

// The UI state a reload carries from the old runtime to the new one (bounded to 1 MiB by the launcher): the viewed identity and screen, display toggles, and
// per-identity drafts, pending send IDs and reading positions (P0c). Everything is validated, and the version-1 format (session-keyed drafts, a line offset) is
// migrated.
//
// pi: apps/p2/src/tui/snapshot.ts

import (
	"sort"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
)

// SnapshotPair is one [key, value] entry of a snapshot's map.
type SnapshotPair[T any] struct {
	Key   string
	Value T
}

// Snapshot is TuiSnapshot. Its maps are lists of pairs in key order (a Go map has none).
type Snapshot struct {
	Version       int
	Session       string
	HasSession    bool
	Conversation  int
	Screen        runtime.Screen
	ShowThinking  bool
	ThinkingSet   bool
	ExpandedTools bool
	Drafts        []SnapshotPair[string]
	Submissions   []SnapshotPair[runtime.Submission]
	Anchors       []SnapshotPair[components.TranscriptAnchor]
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// SnapshotRuntime is snapshotRuntime; it runs on the owner loop.
func SnapshotRuntime(rt *runtime.TuiRuntime) Snapshot {
	rt.SaveState()
	snapshot := Snapshot{
		Version: 2, Session: rt.Session, HasSession: rt.HasSession, Conversation: rt.Conversation, Screen: rt.Screen,
		ShowThinking: rt.ShowThinking, ThinkingSet: rt.ThinkingSetting, ExpandedTools: rt.ExpandedTools,
	}
	for _, key := range sortedKeys(rt.Drafts) {
		snapshot.Drafts = append(snapshot.Drafts, SnapshotPair[string]{key, rt.Drafts[key]})
	}
	for _, key := range sortedKeys(rt.Submissions) {
		snapshot.Submissions = append(snapshot.Submissions, SnapshotPair[runtime.Submission]{key, rt.Submissions[key]})
	}
	for _, key := range sortedKeys(rt.Anchors) {
		if anchor, ok := rt.Anchors[key].(components.TranscriptAnchor); ok {
			snapshot.Anchors = append(snapshot.Anchors, SnapshotPair[components.TranscriptAnchor]{key, anchor})
		}
	}
	return snapshot
}

// migrateKey: version 1 keyed drafts and submissions by session: the main conversation's.
func migrateKey(key string) string {
	if strings.Contains(key, "#") {
		return key
	}
	return runtime.IdentityKey(key, 1)
}

// RestoreRuntime is restoreRuntime: it applies a snapshot (possibly from an older bundle) to rt; it runs on the owner loop before the runtime starts.
func RestoreRuntime(rt *runtime.TuiRuntime, state Snapshot) {
	if state.HasSession {
		rt.Session, rt.HasSession = state.Session, true
	}
	if state.Conversation > 0 {
		rt.Conversation = state.Conversation
	}
	switch state.Screen {
	case "sessions", "chat":
		rt.Screen = state.Screen
	case "models":
		// The model screen became an overlay (P4): back to where it was opened from.
		rt.Screen = "sessions"
		if rt.Session != "" {
			rt.Screen = "chat"
		}
	}
	rt.ShowThinking = state.ShowThinking
	rt.ThinkingSetting = state.ThinkingSet
	rt.ExpandedTools = state.ExpandedTools
	for _, pair := range state.Drafts {
		rt.Drafts[migrateKey(pair.Key)] = pair.Value
	}
	for _, pair := range state.Submissions {
		if pair.Value.Mode == "steer" || pair.Value.Mode == "followUp" {
			rt.Submissions[migrateKey(pair.Key)] = pair.Value
		}
	}
	for _, pair := range state.Anchors {
		anchor := pair.Value
		if anchor.Key == "" {
			continue
		}
		rt.Anchors[pair.Key] = anchor
	}
}
