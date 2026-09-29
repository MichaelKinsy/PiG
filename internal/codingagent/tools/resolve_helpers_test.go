package tools

import "testing"

func mustResolveToCwd(t testing.TB, filePath, cwd string) string {
	t.Helper()
	got, err := resolveToCwd(filePath, cwd)
	if err != nil {
		t.Fatalf("resolveToCwd(%q, %q): %v", filePath, cwd, err)
	}
	return got
}

func mustResolveReadPath(t testing.TB, filePath, cwd string) string {
	t.Helper()
	got, err := resolveReadPath(filePath, cwd)
	if err != nil {
		t.Fatalf("resolveReadPath(%q, %q): %v", filePath, cwd, err)
	}
	return got
}

func mustResolvePath(t testing.TB, cwd, path string) string {
	t.Helper()
	got, err := resolvePath(cwd, path)
	if err != nil {
		t.Fatalf("resolvePath(%q, %q): %v", cwd, path, err)
	}
	return got
}
