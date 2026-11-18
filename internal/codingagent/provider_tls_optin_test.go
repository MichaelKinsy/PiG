package codingagent

import "testing"

// TestProviderTLSOptIn pins the decision itself (D36, owner decision 29): the registration's explicit value wins, a silent registration keeps the file's
// opt-in only for the file's endpoint, and nothing opts in without a true somewhere.
func TestProviderTLSOptIn(t *testing.T) {
	yes, no := true, false
	const fileURL = "https://corp.example/v1"
	for _, tc := range []struct {
		name               string
		file, registration insecureLayer
		want               bool
	}{
		{"nothing says anything", insecureLayer{}, insecureLayer{}, false},
		{"file opts in, no registration", insecureLayer{present: true, setting: &yes, baseURL: fileURL}, insecureLayer{}, true},
		{"file opts in, silent registration without an endpoint", insecureLayer{present: true, setting: &yes, baseURL: fileURL}, insecureLayer{present: true}, true},
		{"file opts in, silent registration at the file's endpoint", insecureLayer{present: true, setting: &yes, baseURL: fileURL}, insecureLayer{present: true, baseURL: fileURL}, true},
		{"file opts in, silent registration at the file's endpoint with a trailing slash", insecureLayer{present: true, setting: &yes, baseURL: fileURL}, insecureLayer{present: true, baseURL: fileURL + "/"}, true},
		{"file opts in, silent registration at its own endpoint", insecureLayer{present: true, setting: &yes, baseURL: fileURL}, insecureLayer{present: true, baseURL: "https://ext.example/v1"}, false},
		{"file opts in, file names no endpoint, silent registration at its own endpoint", insecureLayer{present: true, setting: &yes}, insecureLayer{present: true, baseURL: "https://ext.example/v1"}, false},
		{"file opts in, registration opts in", insecureLayer{present: true, setting: &yes, baseURL: fileURL}, insecureLayer{present: true, setting: &yes}, true},
		{"file opts in, registration opts in at its own endpoint", insecureLayer{present: true, setting: &yes, baseURL: fileURL}, insecureLayer{present: true, setting: &yes, baseURL: "https://ext.example/v1"}, true},
		{"file opts in, registration turns it off", insecureLayer{present: true, setting: &yes, baseURL: fileURL}, insecureLayer{present: true, setting: &no}, false},
		{"file without the setting, silent registration", insecureLayer{present: true, baseURL: fileURL}, insecureLayer{present: true}, false},
		{"file without the setting, registration opts in", insecureLayer{present: true, baseURL: fileURL}, insecureLayer{present: true, setting: &yes}, true},
		{"file false, registration opts in", insecureLayer{present: true, setting: &no, baseURL: fileURL}, insecureLayer{present: true, setting: &yes}, true},
		{"file false, silent registration", insecureLayer{present: true, setting: &no, baseURL: fileURL}, insecureLayer{present: true}, false},
		{"no file entry, registration opts in", insecureLayer{}, insecureLayer{present: true, setting: &yes}, true},
		{"no file entry, registration turns it off", insecureLayer{}, insecureLayer{present: true, setting: &no}, false},
		{"no file entry, silent registration", insecureLayer{}, insecureLayer{present: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := providerTLSOptIn(tc.file, tc.registration); got != tc.want {
				t.Fatalf("providerTLSOptIn = %v, want %v", got, tc.want)
			}
		})
	}
}
