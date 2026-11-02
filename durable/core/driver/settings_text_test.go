// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// The host asks for the settings text before every event: it is encoded again only when the resolved settings changed.
func TestSettingsTextIsEncodedOnlyWhenTheSettingsChange(t *testing.T) {
	settings := &harness.HarnessSettings{}
	s := &coreSession{cfg: SessionConfig{Harness: harness.HarnessOptions{Settings: func() *harness.HarnessSettings { return settings }}}}
	first := s.settingsText()
	if again := s.settingsText(); &again[0] != &first[0] {
		t.Fatal("unchanged settings were encoded again")
	}
	settings = &harness.HarnessSettings{ToolExecution: durable.ToolExecutionSequential}
	changed := s.settingsText()
	if bytes.Equal(changed, first) {
		t.Fatal("changed settings were answered with the old text")
	}
}

// BenchmarkSettingsTextUnchanged is the cost the host pays per event for settings that did not change: a comparison, no encode.
func BenchmarkSettingsTextUnchanged(b *testing.B) {
	settings := &harness.HarnessSettings{ToolExecution: durable.ToolExecutionSequential}
	s := &coreSession{cfg: SessionConfig{Harness: harness.HarnessOptions{Settings: func() *harness.HarnessSettings { return settings }}}}
	s.settingsText()
	b.ReportAllocs()
	for b.Loop() {
		s.settingsText()
	}
}

// BenchmarkSettingsTextEncode is the encode the comparison saves.
func BenchmarkSettingsTextEncode(b *testing.B) {
	settings := &harness.HarnessSettings{ToolExecution: durable.ToolExecutionSequential}
	s := &coreSession{cfg: SessionConfig{Harness: harness.HarnessOptions{Settings: func() *harness.HarnessSettings { return settings }}}}
	st := s.resolveSettings()
	names := extensionNames(st)
	st.Extensions = nil
	b.ReportAllocs()
	for b.Loop() {
		_, _ = json.Marshal(settingsMap(st, names))
	}
}
