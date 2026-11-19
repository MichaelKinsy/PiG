package extensionconformance

// pi: packages/coding-agent/src/core/model-runtime.ts

import (
	"fmt"
	"testing"
)

// insecureSettings lists what the host received as `insecure` in each registration of provider "tls", in order: "omitted" when the registration
// leaves it out, "true" or "false" when it states it.
func (r *modelTypesRig) insecureSettings() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var settings []string
	for _, registration := range r.registrations {
		if registration.Name != "tls" {
			continue
		}
		setting := "omitted"
		if value := registration.Config.InsecureSetting(); value != nil {
			setting = fmt.Sprint(*value)
		}
		settings = append(settings, setting)
	}
	return settings
}

// TestProviderInsecureFalseReachesTheHostAcrossSDKs (D36, owner decision 2026-11-17): a provider registration with `insecure: false` turns an earlier
// opt-in off, so every SDK must send the explicit false and the host must receive it as false, not as an omitted member (an SDK that drops the member
// leaves "omitted" in the second slot, and a host that decodes false as omitted does the same). The tool registers one provider four times (true,
// false, omitted, true), after the factory finished, as Pi's pi.registerProvider does (types.ts:1766-1803). The Node runtime sends its merged root, so
// its third registration carries the false the second one set; every other SDK sends only what the extension wrote, which leaves insecure out. Either
// way the effective value Pi's merge computes (model-runtime.ts:970-977: a defined value replaces the earlier one, an omitted one keeps it) is
// true, false, false, true.
func TestProviderInsecureFalseReachesTheHostAcrossSDKs(t *testing.T) {
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		r.toolWith("insecure_provider", map[string]any{})
		settings := r.insecureSettings()
		if len(settings) != 4 {
			t.Fatalf("the host received %d registrations of the provider, want 4: %v", len(settings), settings)
		}
		if settings[0] != "true" || settings[1] != "false" || settings[3] != "true" {
			t.Fatalf("insecure as the host received it = %v, want [true false omitted-or-false true]", settings)
		}
		if settings[2] == "true" {
			t.Fatalf("a registration that leaves insecure out arrived as true: %v", settings)
		}
		effective := false
		var effectives []bool
		for _, setting := range settings {
			if setting != "omitted" {
				effective = setting == "true"
			}
			effectives = append(effectives, effective)
		}
		if want := []bool{true, false, false, true}; fmt.Sprint(effectives) != fmt.Sprint(want) {
			t.Fatalf("effective insecure after each registration = %v, want %v (received %v)", effectives, want, settings)
		}
	})
}
