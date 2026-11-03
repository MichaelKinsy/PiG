// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver

import (
	"bytes"
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
