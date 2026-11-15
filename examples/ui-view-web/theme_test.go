//go:build linux || darwin

package uiviewweb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPageIsIndexHTMLWithoutATheme: a bridge started without --theme serves index.html byte for byte, and a path under
// /theme/ is refused as every path the bridge does not serve is.
func TestPageIsIndexHTMLWithoutATheme(t *testing.T) {
	b := connectBridge(t, t.TempDir())
	want, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	if status, body := get(t, b.base+"/"); status != 200 || body != string(want) {
		t.Errorf("GET / = %d, %d bytes; want index.html as it is, %d bytes", status, len(body), len(want))
	}
	if status, _ := get(t, b.base+"/theme/theme.css"); status != 403 {
		t.Errorf("GET /theme/theme.css without --theme = %d, want 403", status)
	}
	if status, _ := get(t, b.base+"/theme/theme.css", "Authorization", b.auth(t)); status != 404 {
		t.Errorf("GET /theme/theme.css without --theme, with the secret = %d, want 404", status)
	}
}

// TestThemeDirectoryIsLinkedAndServed: with --theme the page is index.html plus one stylesheet link at the end of its
// head, the directory's files are served by name with the type of their extension, and no other file is.
func TestThemeDirectoryIsLinkedAndServed(t *testing.T) {
	dir := t.TempDir()
	css := ":root { --pig-accent: #123456; }\n"
	for name, content := range map[string]string{"theme.css": css, "mark.svg": "<svg xmlns=\"http://www.w3.org/2000/svg\"/>\n", "notes.txt": "not served\n", ".hidden.css": "not served\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "outside.css"), []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := connectBridge(t, t.TempDir(), "--theme", dir)
	index, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	link := "<link rel=\"stylesheet\" href=\"theme/theme.css\">\n"
	want := strings.Replace(string(index), "</head>", link+"</head>", 1)
	if want == string(index) {
		t.Fatal("index.html has no </head>")
	}
	if status, body := get(t, b.base+"/"); status != 200 || body != want {
		t.Errorf("GET / = %d, %d bytes; want index.html with the theme's link before </head>, %d bytes", status, len(body), len(want))
	}
	if status, body := get(t, b.base+"/theme/theme.css"); status != 200 || body != css {
		t.Errorf("GET /theme/theme.css = %d %q", status, body)
	}
	if status, body := get(t, b.base+"/theme/mark.svg"); status != 200 || !strings.HasPrefix(body, "<svg") {
		t.Errorf("GET /theme/mark.svg = %d %q", status, body)
	}
	for _, path := range []string{"/theme/notes.txt", "/theme/.hidden.css", "/theme/absent.css", "/theme/%2e%2e%2foutside.css", "/theme/..%2foutside.css", "/theme/sub/theme.css", "/theme/"} {
		if status, _ := get(t, b.base+path); status != 404 {
			t.Errorf("GET %s = %d, want 404", path, status)
		}
	}
	if status, _ := get(t, b.base+"/theme/theme.css", "Origin", "http://example.com"); status != 403 {
		t.Errorf("GET /theme/theme.css from another origin = %d, want 403", status)
	}
}
