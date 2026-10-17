package endpoints

import (
	"regexp"
	"testing"
)

func TestLazyRegexpMatchesLikeRegexp(t *testing.T) {
	const expr = "^(us|eu|ap|sa|ca|me|af|il|mx)\\-\\w+\\-\\d+$"
	lazy, eager := NewLazyRegexp(expr), regexp.MustCompile(expr)
	for _, region := range []string{"us-east-1", "eu-west-3", "cn-north-1", "us-gov-west-1", "", "us-east", "xx-east-1"} {
		if got, want := lazy.MatchString(region), eager.MatchString(region); got != want {
			t.Errorf("%q: lazy %v, eager %v", region, got, want)
		}
	}
}
