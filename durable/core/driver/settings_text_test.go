// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver

import (
	"bytes"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Pi reads HarnessSettings at every use and resolves them afresh (packages/durable/src/harness/harness.ts:236 `settings = () => resolveSettings(hostSettings())`),
// so a change to the settings object the host returned reaches the core at the next event, whether the host replaced the object or
// changed a map in it in place.
func TestSettingsTextFollowsTheSettingsTheHostReturns(t *testing.T) {
	headers := map[string]string{"x-one": "1"}
	metadata := map[string]any{"nested": map[string]any{"k": "v"}}
	settings := &harness.HarnessSettings{Stream: &durable.ConversationStreamOptions{Headers: headers, Metadata: metadata}}
	s := &coreSession{cfg: SessionConfig{Harness: harness.HarnessOptions{Settings: func() *harness.HarnessSettings { return settings }}}}
	first := append([]byte(nil), s.settingsText()...)
	if again := s.settingsText(); !bytes.Equal(again, first) {
		t.Fatalf("unchanged settings encoded differently: %s, then %s", first, again)
	}

	headers["x-two"] = "2"
	if got := s.settingsText(); !strings.Contains(string(got), `"x-two":"2"`) {
		t.Fatalf("a header added in place did not reach the settings text: %s", got)
	}
	metadata["nested"].(map[string]any)["k"] = "changed"
	if got := s.settingsText(); !strings.Contains(string(got), `"k":"changed"`) {
		t.Fatalf("a nested metadata value changed in place did not reach the settings text: %s", got)
	}

	settings = &harness.HarnessSettings{ToolExecution: durable.ToolExecutionSequential}
	if changed := s.settingsText(); bytes.Equal(changed, first) || !strings.Contains(string(changed), "sequential") {
		t.Fatalf("replaced settings were answered with the old text: %s", changed)
	}
}
