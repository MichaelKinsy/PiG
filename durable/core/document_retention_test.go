// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// settings.documentRetentionMs (harness/agent.ts resolveSettings) reaches the Session: the host's number as given, none for a
// host that set none.
func TestTheSessionReadsTheDocumentRetentionOfTheOpenSettings(t *testing.T) {
	settings := jv.NewObject()
	open := jv.NewObject()
	open.Set("settings", settings)
	h := &handler{open: open}
	if got := h.documentRetentionMs(); got != nil {
		t.Fatalf("no setting: %v, want nil", *got)
	}
	settings.Set("documentRetentionMs", 250.0)
	if got := h.documentRetentionMs(); got == nil || *got != 250 {
		t.Fatalf("setting 250: %v", got)
	}
	settings.Set("documentRetentionMs", 0.0)
	if got := h.documentRetentionMs(); got == nil || *got != 0 {
		t.Fatalf("setting 0: %v", got)
	}
}
