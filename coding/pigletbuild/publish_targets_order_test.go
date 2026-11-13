package pigletbuild

import (
	"fmt"
	"maps"
	"slices"
	"testing"
)

// The targets of a publish are the keys of a map, listed by name. No two keys are equal, and two have one name only when
// an OS or an architecture holds a slash; sortedTargets then leaves them as slices.SortedFunc did.
func TestSortedTargetsOrder(t *testing.T) {
	if got := sortedTargets(maps.Keys(map[Target]string{})); got != nil {
		t.Fatalf("sortedTargets of no target = %#v, want nil", got)
	}
	found := map[Target]string{
		{OS: "windows", Arch: "amd64"}: "", {OS: "linux", Arch: "arm64"}: "", {OS: "darwin", Arch: "arm64"}: "", {OS: "linux", Arch: "amd64"}: "",
	}
	want := []Target{{OS: "darwin", Arch: "arm64"}, {OS: "linux", Arch: "amd64"}, {OS: "linux", Arch: "arm64"}, {OS: "windows", Arch: "amd64"}}
	if got := sortedTargets(maps.Keys(found)); !slices.Equal(got, want) {
		t.Fatalf("sortedTargets = %v, want %v", got, want)
	}

	// Forty targets under ten names, each name spelled two ways, in a scattered order: past the twelve elements an insertion sort takes, with equal keys.
	var many []Target
	for i := range 40 {
		name := fmt.Sprintf("n%d", i*7%10)
		if i%2 == 0 {
			many = append(many, Target{OS: "os/" + name, Arch: "arch"})
		} else {
			many = append(many, Target{OS: "os", Arch: name + "/arch"})
		}
	}
	for name, in := range map[string][]Target{
		"two spellings of one name":           {{OS: "a/b", Arch: "c"}, {OS: "a", Arch: "b/c"}},
		"two spellings of one name, reversed": {{OS: "a", Arch: "b/c"}, {OS: "a/b", Arch: "c"}},
		"forty targets under ten names":       many,
	} {
		if got, want := sortedTargets(slices.Values(in)), slices.SortedFunc(slices.Values(in), compareTargets); !slices.Equal(got, want) {
			t.Errorf("%s: sortedTargets = %v, slices.SortedFunc = %v", name, got, want)
		}
	}
}
