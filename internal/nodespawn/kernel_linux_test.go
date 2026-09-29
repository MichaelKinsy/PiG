package nodespawn

import "testing"

func TestReleaseAtLeast(t *testing.T) {
	for _, c := range []struct {
		release string
		want    bool
	}{
		{"6.8.0-137-generic", false},
		{"6.16.12", false},
		{"6.17.0-1010-azure", true},
		{"6.17", true},
		{"6.18.3+", true},
		{"7.0.0", true},
		{"10.1", true},
		{"5.99.0", false},
		{"6", false},
		{"", false},
	} {
		if got := releaseAtLeast(c.release, 6, 17); got != c.want {
			t.Errorf("releaseAtLeast(%q, 6, 17) = %v, want %v", c.release, got, c.want)
		}
	}
}
