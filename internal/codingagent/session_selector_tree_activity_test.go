package codingagent

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// session-selector.ts:252-282 computes maxima recursively and stably sorts both roots and children using Date.getTime().
func TestSessionTreeLatestActivityBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sessions []SessionInfo
		want     []string
	}{
		{"empty", nil, []string{}},
		{"singleton", []SessionInfo{{Path: "one", ID: "one", Modified: time.UnixMilli(0)}}, []string{"one"}},
		{"latest grandchild orders siblings and roots", []SessionInfo{
			{Path: "other", ID: "other", Modified: time.UnixMilli(3)},
			{Path: "parent", ID: "parent", Modified: time.UnixMilli(0)},
			{Path: "child-one", ID: "child-one", ParentSession: "parent", Modified: time.UnixMilli(2)},
			{Path: "child-two", ID: "child-two", ParentSession: "parent", Modified: time.UnixMilli(1)},
			{Path: "grandchild", ID: "grandchild", ParentSession: "child-two", Modified: time.UnixMilli(4)},
		}, []string{"parent", "child-two", "grandchild", "child-one", "other"}},
		{"stable equal activity", []SessionInfo{{Path: "b", ID: "b", Modified: time.UnixMilli(1)}, {Path: "a", ID: "a", Modified: time.UnixMilli(1)}}, []string{"b", "a"}},
		{"Date millisecond precision", []SessionInfo{{Path: "b", ID: "b", Modified: time.Unix(0, 1)}, {Path: "a", ID: "a", Modified: time.Unix(0, 2)}}, []string{"b", "a"}},
		{"dates outside UnixNano range", []SessionInfo{{Path: "old", ID: "old", Modified: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, {Path: "new", ID: "new", Modified: time.Date(2400, 1, 1, 0, 0, 0, 0, time.UTC)}}, []string{"new", "old"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := []string{}
			for _, node := range flattenSessionTree(buildSessionTree(tc.sessions)) {
				got = append(got, node.Session.ID)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("order=%v want=%v", got, tc.want)
			}
		})
	}
}

func BenchmarkSessionTreeLatestActivity(b *testing.B) {
	for _, count := range []int{10, 1000, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			dir := b.TempDir()
			sessions := make([]SessionInfo, count)
			for i := range sessions {
				sessions[i] = SessionInfo{Path: filepath.Join(dir, fmt.Sprintf("%d.jsonl", i)), Modified: time.UnixMilli(int64(i))}
				if i%10 != 0 {
					sessions[i].ParentSession = sessions[i-i%10].Path
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				nodes := flattenSessionTree(buildSessionTree(sessions))
				if len(nodes) != len(sessions) {
					b.Fatalf("nodes=%d want=%d", len(nodes), len(sessions))
				}
			}
		})
	}
}
