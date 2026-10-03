package bytesize

import "testing"

func TestParse(t *testing.T) {
	for input, want := range map[string]int64{"0": 0, "12": 12, "5GiB": 5 << 30, "5 gb": 5_000_000_000, "3MiB": 3 << 20, "2kb": 2000, "7B": 7} {
		if got, err := Parse(input); err != nil || got != want {
			t.Errorf("Parse(%q) = %d, %v; want %d", input, got, err, want)
		}
	}
	for _, input := range []string{"", "-1", "abc", "1TB", "9999999999GiB"} {
		if _, err := Parse(input); err == nil {
			t.Errorf("Parse(%q) succeeded", input)
		}
	}
}
