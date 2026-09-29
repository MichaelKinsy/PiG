package ai

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCopilotDeviceResponseFieldPresence(t *testing.T) {
	// Pi startDeviceFlow checks field types before trusting the browser URL, and accepts empty string codes or an explicit zero expiry.
	for _, tc := range []struct {
		name, body, wantError string
	}{
		{"null response", `null`, "Invalid device code response"},
		{"missing expiry", `{"device_code":"d","user_code":"u","verification_uri":"https://github.com/login/device"}`, "Invalid device code response fields"},
		{"missing URI", `{"device_code":"d","user_code":"u","expires_in":900}`, "Invalid device code response fields"},
		{"null interval", `{"device_code":"d","user_code":"u","verification_uri":"https://github.com/login/device","expires_in":900,"interval":null}`, "Invalid device code response fields"},
		{"empty codes and zero expiry", `{"device_code":"","user_code":"","verification_uri":"https://github.com/login/device","expires_in":0}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withMockCopilotClient(t, func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})
			got, err := startDeviceFlow(t.Context(), "github.com")
			if tc.wantError != "" {
				if err == nil || err.Error() != tc.wantError {
					t.Fatalf("response=%#v error=%v want=%s", got, err, tc.wantError)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}
