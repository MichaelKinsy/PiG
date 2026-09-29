// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package coding

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
)

// Like the live upstream file, these cases assert extension and Session state,
// not model answer quality. Complete faux responses keep the assertions hermetic.
func TestCompactionExtensionsUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		prompts    int
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/compaction-extensions.test.ts:124
		{"should emit before_compact and compact events", "default", 2},
		// .upstream/v0.87.1/packages/coding-agent/test/compaction-extensions.test.ts:160
		{"should allow extensions to cancel compaction", "cancel", 1},
		// .upstream/v0.87.1/packages/coding-agent/test/compaction-extensions.test.ts:173
		{"should allow extensions to provide custom compaction", "custom", 2},
		// .upstream/v0.87.1/packages/coding-agent/test/compaction-extensions.test.ts:210
		{"should include entries in compact event after compaction is saved", "saved", 1},
		// .upstream/v0.87.1/packages/coding-agent/test/compaction-extensions.test.ts:231
		{"should continue with default compaction if extension throws error", "throw", 1},
		// .upstream/v0.87.1/packages/coding-agent/test/compaction-extensions.test.ts:278
		{"should call multiple extensions in order", "order", 1},
		// .upstream/v0.87.1/packages/coding-agent/test/compaction-extensions.test.ts:353
		{"should pass correct data in before_compact event", "data", 2},
		// .upstream/v0.87.1/packages/coding-agent/test/compaction-extensions.test.ts:391
		{"should use extension compaction even with different values", "values", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &scriptedProvider{}
			for range 8 {
				p.responses = append(p.responses, fauxReply("complete summary", ai.StopReasonStop, 0))
			}
			model := fakeModelWithProvider(p)
			model.ID = "faux-1"
			model.Capabilities.ContextWindow = 200000
			model.Capabilities.MaxOutputTokens = 8192
			s, err := NewSession(newTestServicesSmallKeep(t), SessionOptions{Model: model, SystemPrompt: "You are a helpful assistant. Be concise."})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			var before []extension.SessionBeforeCompactEvent
			var after []extension.SessionCompactEvent
			var order []string
			var savedAtAfter bool
			makeExtension := func(name string) extension.Extension {
				return extension.Extension{Path: name, Handlers: map[string][]extension.HandlerFn{
					"session_before_compact": {func(args ...any) (any, error) {
						event := args[0].(extension.SessionBeforeCompactEvent)
						before = append(before, event)
						order = append(order, name+"-before")
						switch tc.mode {
						case "cancel":
							return extension.SessionBeforeCompactResult{Cancel: true}, nil
						case "throw":
							return nil, errors.New("Extension intentionally throws")
						case "custom", "values":
							prep := event.Preparation.(*compaction.CompactionPreparation)
							summary := "Custom summary from extension"
							tokens := prep.TokensBefore
							if tc.mode == "values" {
								summary = "Custom summary with modified values"
								tokens = 999
							}
							return extension.SessionBeforeCompactResult{Compaction: map[string]any{"summary": summary, "firstKeptEntryId": prep.FirstKeptEntryID, "tokensBefore": tokens}}, nil
						}
						return nil, nil
					}},
					"session_compact": {func(args ...any) (any, error) {
						after = append(after, args[0].(extension.SessionCompactEvent))
						order = append(order, name+"-after")
						for _, entry := range s.inner.Entries() {
							if entry.Base.Type == "compaction" {
								savedAtAfter = true
							}
						}
						return nil, nil
					}},
				}}
			}
			extensions := []extension.Extension{makeExtension("extension1")}
			if tc.mode == "order" {
				extensions = append(extensions, makeExtension("extension2"))
			}
			s.ReplaceRunner(inproc.NewRunner(extensions, t.TempDir()))
			for _, prompt := range []string{"What is 2+2? Reply with just the number.", "What is 3+3? Reply with just the number."}[:tc.prompts] {
				if _, err := s.Send(t.Context(), prompt); err != nil {
					t.Fatal(err)
				}
				drainEvents(t, s)
			}
			result, err := s.CompactResult(t.Context(), "")
			if tc.mode == "cancel" {
				if err == nil || !strings.Contains(err.Error(), "Compaction cancelled") || len(after) != 0 {
					t.Fatalf("error=%v after=%v", err, after)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.Summary == "" {
				t.Fatal("empty summary")
			}
			if tc.mode == "order" {
				want := []string{"extension1-before", "extension2-before", "extension1-after", "extension2-after"}
				if !reflect.DeepEqual(order, want) {
					t.Fatalf("order=%v", order)
				}
				return
			}
			if len(before) != 1 || len(after) != 1 {
				t.Fatalf("before=%d after=%d", len(before), len(after))
			}
			prep, ok := before[0].Preparation.(*compaction.CompactionPreparation)
			if !ok || prep.MessagesToSummarize == nil || prep.TurnPrefixMessages == nil || prep.FirstKeptEntryID == "" || prep.TokensBefore < 0 || before[0].BranchEntries == nil {
				t.Fatalf("preparation=%+v", before[0])
			}
			raw, err := json.Marshal(after[0].CompactionEntry)
			if err != nil {
				t.Fatal(err)
			}
			var entry icodingagent.CompactionEntry
			if err := json.Unmarshal(raw, &entry); err != nil {
				t.Fatal(err)
			}
			if entry.Type != "compaction" || entry.Summary == "" || entry.TokensBefore < 0 || !savedAtAfter {
				t.Fatalf("entry=%+v saved=%v", entry, savedAtAfter)
			}
			fromExtension := tc.mode == "custom" || tc.mode == "values"
			if after[0].FromExtension != fromExtension {
				t.Fatalf("fromExtension=%v", after[0].FromExtension)
			}
			if fromExtension {
				want := "Custom summary from extension"
				if tc.mode == "values" {
					want = "Custom summary with modified values"
					if result.TokensBefore != 999 || entry.TokensBefore != 999 {
						t.Fatal("extension token value changed")
					}
				}
				if result.Summary != want || entry.Summary != want {
					t.Fatal("extension summary changed")
				}
			}
			if tc.mode == "data" {
				// Go combines request authentication with preparation on the Session's runtime.
				runtime := s.services.ModelRuntime()
				if runtime == nil || len(s.inner.Entries()) == 0 {
					t.Fatal("session runtime unavailable")
				}
				if _, _, _, err := runtime.prepareRequest(t.Context(), s.Model(), ai.StreamOptions{}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
