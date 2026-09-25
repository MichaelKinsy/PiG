package shellconfig

import (
	"strings"
	"testing"
)

func TestIsLegacyWSLBashPath(t *testing.T) {
	for path, want := range map[string]bool{
		`C:\Windows\System32\bash.exe`:      true,
		`c:/windows/sysnative/bash.exe`:     true,
		`C:\Program Files\Git\bin\bash.exe`: false,
		`/bin/bash`:                         false,
	} {
		if got := IsLegacyWSLBashPath(path); got != want {
			t.Errorf("IsLegacyWSLBashPath(%q) = %v, want %v", path, got, want)
		}
	}
	cfg := ForBash(`C:\Windows\System32\bash.exe`)
	if strings.Join(cfg.Args, " ") != "-s" || cfg.CommandTransport != "stdin" {
		t.Fatalf("legacy WSL cfg = %+v", cfg)
	}
}
