// SPDX-License-Identifier: MIT

package spec

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
)

// TestGeneratedFilesAreCurrent fails when a table changed without go generate ./durable/core/abi.
func TestGeneratedFilesAreCurrent(t *testing.T) {
	if abi.ID != ID() {
		t.Fatalf("abi.ID %s is stale; tables hash to %s (go generate ./durable/core/abi)", abi.ID, ID())
	}
	have, err := os.ReadFile("../abi.json")
	if err != nil {
		t.Fatal(err)
	}
	want, err := JSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(have, want) {
		t.Fatal("abi.json is stale (go generate ./durable/core/abi)")
	}
}

// TestTablesUnchanged pins abi_id so a table change is deliberate: the ID changes in the same commit as the ABI document.
// durable-s0 (58b037a23) froze f82162f6...; the lead's 2026-10-07 ruling added the resume event, renamed snapshotAsOf's
// argument to the entry ID `at` and let watch.open name a conversation view; the hop-chain ruling added the hopped event, the hops effect and the step order list (ABI.md section 5.3); the crossing work folded out_ptr into step, which now returns the address of the self-sized step; the facade-tick work added the waitForIdle api op (scheduler.ts waitForIdle answers an idle scope in the same microtask checkpoint); the memory work made step return a part list, so a model request reaches the host without a copy in the output buffer; the hop work put a commit on the hops chain that spends its hops (applied after hop `at`, not in a step of its own) and added the `hop` export, which answers a hopped event with an integer when the step has nothing to decode; the count gates added alloc_stats (bytes allocated and allocations, no collection), then its third counter, completed collections (NumGC), and the allocStats layout row; the schema-prelude ruling let a step carry commits before its reads (the host applies them first), so cold open finds the schema by identifier as Pi's applySqliteMigrations does; the Pi 4ac0bd8c port added the ownedTasks api op (TaskRuntime.ownedTasks), which the scheduler serves from its ownership index; the document retention port added the doc_cache notice (a session-mode core's document cache sizes, replicated by the host's documentCacheSizes); the Pi e17e2e10 port (durable sleeping state) turned the timer effect's durable flag into a class (volatile, durable, background), replaced the sleep api op by sleepUntil, added the wake api op and the wake notice; the Pi 482c1381 port (crash-loop guard, report sources) added the retryTask api op and gave the report notice an info member.
func TestTablesUnchanged(t *testing.T) {
	const pinned = "1b078df222ac3e4af422e2649b282307deb5a18436305d7686f19fcfc47501c0"
	if ID() != pinned {
		t.Fatalf("abi_id %s differs from the pinned %s", ID(), pinned)
	}
}

func TestIDIsContentIdentity(t *testing.T) {
	id := ID()
	if len(id) != 64 || strings.Trim(id, "0123456789abcdef") != "" {
		t.Fatalf("abi_id %q is not 64 hex digits", id)
	}
	old := eventRows[0].Layout
	eventRows[0].Layout += "!"
	changed := ID()
	eventRows[0].Layout = old
	if changed == id || ID() != id {
		t.Fatal("a table change must change the ID and restoring it must restore the ID")
	}
}
