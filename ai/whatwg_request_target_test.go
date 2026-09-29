package ai

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

func TestWHATWGRequestTargetSurvivesGoHTTPSerialization(t *testing.T) {
	for _, path := range []string{"/a|b/v1/chat/completions", "/a%b/v1/chat/completions", "/a%zzb/v1/chat/completions"} {
		t.Run(path, func(t *testing.T) {
			serialized := "http://example.test" + path
			parsed, err := nodeurl.RequestURL(serialized)
			if err != nil {
				t.Fatal(err)
			}
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "", strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			request.URL, request.Host = parsed, parsed.Host
			prepared := originFormRequest(request)
			var wire bytes.Buffer
			if err := prepared.Write(&wire); err != nil {
				t.Fatal(err)
			}
			line, _, _ := strings.Cut(wire.String(), "\r\n")
			if want := "POST " + path + " HTTP/1.1"; line != want {
				t.Fatalf("request line=%q want=%q", line, want)
			}
			if request.URL.String() != serialized || request.URL.Opaque != parsed.Opaque {
				t.Fatal("origin adaptation mutated the caller's URL")
			}
		})
	}
}
