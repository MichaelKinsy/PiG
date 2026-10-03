package testenv

import (
	"strings"
	"testing"
)

func TestShortTempBase(t *testing.T) {
	const localAppData = `C:\Users\runner\AppData\Local`
	longTemp := `D:\a\_temp\` + strings.Repeat("d", maxShortTempDirBytes)
	for _, test := range []struct {
		name, goos, temp, localAppData, want string
	}{
		{"unix uses /tmp whatever TMPDIR is", "darwin", "/var/folders/zz/" + strings.Repeat("d", 80), localAppData, "/tmp"},
		{"windows keeps a short temp directory", "windows", `C:\T`, localAppData, `C:\T`},
		{"windows keeps a temp directory that just fits", "windows", `D:\` + strings.Repeat("d", maxShortTempDirBytes-len(`D:\`)-len(`\pe`)-10), localAppData, `D:\` + strings.Repeat("d", maxShortTempDirBytes-len(`D:\`)-len(`\pe`)-10)},
		{"windows falls back when the temp directory is one byte too long", "windows", `D:\` + strings.Repeat("d", maxShortTempDirBytes-len(`D:\`)-len(`\pe`)-10+1), localAppData, localAppData + `\pig\s`},
		{"windows falls back from a long temp directory", "windows", longTemp, localAppData, localAppData + `\pig\s`},
		{"windows without local app data keeps the long temp directory", "windows", longTemp, "", longTemp},
	} {
		if got := shortTempBase(test.goos, test.temp, test.localAppData, "pe"); strings.ReplaceAll(got, "/", `\`) != strings.ReplaceAll(test.want, "/", `\`) {
			t.Errorf("%s: shortTempBase = %q, want %q", test.name, got, test.want)
		}
	}
}
