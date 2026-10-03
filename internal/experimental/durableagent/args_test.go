package durableagent

import "testing"

// main.ts:5-11: --continue and -c select the newest session; any other argument is an error that names it.
func TestParseArgs(t *testing.T) {
	for _, test := range []struct {
		args      []string
		continued bool
		err       string
	}{
		{nil, false, ""},
		{[]string{"--continue"}, true, ""},
		{[]string{"-c"}, true, ""},
		{[]string{"-c", "--continue"}, true, ""},
		{[]string{"--cont"}, false, "Unknown argument: --cont"},
		{[]string{"-c", "extra"}, false, "Unknown argument: extra"},
		{[]string{""}, false, "Unknown argument: "},
	} {
		options, err := ParseArgs(test.args)
		if test.err != "" {
			if err == nil || err.Error() != test.err {
				t.Errorf("ParseArgs(%q) error = %v, want %q", test.args, err, test.err)
			}
			continue
		}
		if err != nil || options.ContinueSession != test.continued {
			t.Errorf("ParseArgs(%q) = %+v, %v; want continue %v", test.args, options, err, test.continued)
		}
	}
}
