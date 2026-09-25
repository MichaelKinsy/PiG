package piglet

import (
	"strings"
	"testing"
)

func TestValidateRemoteSourceRejectsAuthenticationMaterial(t *testing.T) {
	for _, source := range []string{
		"npm:@acme/porter@1.2.3",
		"git:github.com/acme/porter@v1.2.3",
		"git:github.com/acme/porter@0123456789abcdef0123456789abcdef01234567",
		"git:ssh://git@github.com/acme/porter@v1.2.3",
	} {
		if err := ValidateRemoteSource(source); err != nil {
			t.Errorf("ValidateRemoteSource(%q) = %v", source, err)
		}
	}
	for source, want := range map[string]string{
		"git:https://user:token@github.com/acme/porter@v1": "must not include credentials",
		"git:https://token@github.com/acme/porter@v1":      "must not include credentials",
		"git:https://github.com/acme/porter?token=x@v1":    "must not include query parameters",
		"local:./porter": "must use npm or Git",
		"./porter.yaml":  "must use npm or Git",
		"porter":         "explicit scheme",
		"catalog:porter": "unsupported source scheme",
		"":               "source is required",
	} {
		if err := ValidateRemoteSource(source); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ValidateRemoteSource(%q) error = %v, want %q", source, err, want)
		}
	}
}

func TestValidateRemoteSourceParserErrorsDoNotEchoCredentials(t *testing.T) {
	const secret = "distribution-parser-secret"
	for _, raw := range []string{
		"git:https://user:" + secret + "@github.com/acme",
		"git:https://" + secret + "@github.com/acme",
		"git:https://github.com/acme?token=" + secret,
		"npm:@acme/porter?registry=https%3A%2F%2Fuser%3A" + secret + "%40registry.example",
		secret + ":unsupported",
		secret,
	} {
		err := ValidateRemoteSource(raw)
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Errorf("parser error exposes rejected source: %v", err)
		}
	}
}

func TestDistributionOfflineHonorsPiAndPigOfflineSettings(t *testing.T) {
	for _, tc := range []struct {
		pi, pig string
		want    bool
	}{
		{"", "", false},
		{"", "1", true},
		{"", "yes", true},
		{"", "0", false},
		{"1", "", true},
		{"0", "", true},
	} {
		t.Setenv("PI_OFFLINE", tc.pi)
		t.Setenv("PIG_OFFLINE", tc.pig)
		if got := DistributionOffline(); got != tc.want {
			t.Errorf("PI_OFFLINE=%q PIG_OFFLINE=%q: DistributionOffline() = %v, want %v", tc.pi, tc.pig, got, tc.want)
		}
	}
}
