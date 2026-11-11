package contract

import "testing"

// Pi main 8c9a5cd9b (apps/p2/test/p2/radius.test.ts, "a shell page saved before the shell's URLs dropped /brains is read in the current form").
func TestCurrentShellPageReadsAPageSavedWithBrainsInTheCurrentForm(t *testing.T) {
	const server = "11111111-0000-4000-8000-000000000000"
	for saved, want := range map[string]string{
		"https://radius.earendil.com/pi/brains/org_1/" + server: "https://radius.earendil.com/pi/org_1/" + server,
		"https://radius.earendil.com/pi/org_1/" + server:        "https://radius.earendil.com/pi/org_1/" + server,
		"":                                                      "",
	} {
		if got := CurrentShellPage(saved); got != want {
			t.Errorf("CurrentShellPage(%q) = %q, want %q", saved, got, want)
		}
	}
}
