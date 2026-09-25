package main

import "testing"

// `pig piglet publish` reaches the GitHub Release publisher, which rejects a
// contradictory request as a usage error before it reads any file or runs gh.
// The Piglet source command table has no publish verb and would exit 1.
func TestPigletPublishRoutesToReleasePublisher(t *testing.T) {
	if code := runPigPreSessionCommand([]string{"piglet", "publish", "porter.yaml", "--to", "github", "--yes", "--dry-run"}); code != 2 {
		t.Fatalf("pig piglet publish --yes --dry-run exit = %d, want usage error 2", code)
	}
}
