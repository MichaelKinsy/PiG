package mcpext

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// The OAuth callback page shows the provider's error and error_description, which the browser request supplies. The page is HTML, so the render function must escape every interpolated value (Pi: packages/ai/src/utils/oauth-page.ts escapeHtml on title, heading, message and details).
func TestOAuthCallbackPageEscapesRequestValues(t *testing.T) {
	const payload = `<script>alert(1)</script>"'&`
	server, err := listenForCallback(callbackSettings{host: "127.0.0.1", path: "/callback"}, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()

	cases := []struct {
		name  string
		query url.Values
	}{
		{"error_description", url.Values{"error": {"access_denied"}, "error_description": {payload}}},
		{"error without a description", url.Values{"error": {payload}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registered := make(chan struct{})
			failed := make(chan error, 1)
			go func() {
				_, err := server.WaitForCallback(oauth.WithCallbackRegistered(t.Context(), func() { close(registered) }), "state-"+tc.name, "")
				failed <- err
			}()
			select {
			case <-registered:
			case err := <-failed:
				t.Fatalf("the wait ended before it registered: %v", err)
			}
			tc.query.Set("state", "state-"+tc.name)
			response, err := http.Get(server.RedirectURL + "?" + tc.query.Encode()) //nolint:noctx // loopback test server
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if got := response.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
				t.Fatalf("content type = %q", got)
			}
			page := string(body)
			if strings.Contains(page, "<script>") || strings.Contains(page, "alert(1)</script>") {
				t.Fatalf("the callback page reflects the request's markup unescaped:\n%s", page)
			}
			if !strings.Contains(page, "&lt;script&gt;alert(1)&lt;/script&gt;&quot;&#39;&amp;") {
				t.Fatalf("the callback page does not show the escaped value:\n%s", page)
			}
			if err := <-failed; err == nil {
				t.Fatal("the failed authorization did not reject the wait")
			}
		})
	}
}
