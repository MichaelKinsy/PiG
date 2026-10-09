package coding

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// nested-tool-calls.ts:62-72: arguments are omitted only when their JSON is longer than maxArgumentBytesPerCall or
// takes the recorded total past maxArgumentBytesTotal; arguments of exactly either size are kept. Pi's tests check
// only sizes well over the limits.
func TestNestedCallRecorderKeepsArgumentsOfExactlyTheLimits(t *testing.T) {
	// `{"t":""}` is 8 bytes.
	args := func(bytes int) ai.JsonObject { return ai.JsonObject{"t": strings.Repeat("x", bytes-8)} }
	recorder := NewNestedCallRecorder()
	for i := range NestedCallLimits.MaxArgumentBytesTotal / NestedCallLimits.MaxArgumentBytesPerCall {
		record := recorder.Start(recorderCall("c", args(NestedCallLimits.MaxArgumentBytesPerCall)))
		if record.Arguments == nil || record.ArgumentsBytes != nil {
			t.Fatalf("call %d of %d bytes: arguments kept %t, argumentsBytes %v", i, NestedCallLimits.MaxArgumentBytesPerCall, record.Arguments != nil, record.ArgumentsBytes)
		}
		recorder.Finish(record, false, "")
	}
	if !recorder.Snapshot().Complete {
		t.Fatalf("a total of exactly %d bytes is incomplete", NestedCallLimits.MaxArgumentBytesTotal)
	}
	over := recorder.Start(recorderCall("d", ai.JsonObject{}))
	if over.Arguments != nil || over.ArgumentsBytes == nil || *over.ArgumentsBytes != 2 || recorder.Snapshot().Complete {
		t.Fatalf("call past the total: arguments %v, argumentsBytes %v, complete %t", over.Arguments, over.ArgumentsBytes, recorder.Snapshot().Complete)
	}

	single := NewNestedCallRecorder()
	record := single.Start(recorderCall("e", args(NestedCallLimits.MaxArgumentBytesPerCall+1)))
	if record.Arguments != nil || record.ArgumentsBytes == nil || *record.ArgumentsBytes != NestedCallLimits.MaxArgumentBytesPerCall+1 {
		t.Fatalf("call one byte over: arguments %v, argumentsBytes %v", record.Arguments, record.ArgumentsBytes)
	}
}
