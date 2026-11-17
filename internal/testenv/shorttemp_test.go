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

// The directory ShortTempDir makes on Windows is the temp directory, the prefix and the ten digits os.MkdirTemp adds; it is kept when that is at most maxShortTempDirBytes (70) and the base falls back to %LOCALAPPDATA%\pig\s from 71. The longest socket path below it is \pig\h<uint32>\<count>.s, 26 bytes (28 before the shorter leaf), so a directory of 70 bytes holds a path of 96 against the 107 a sun_path allows, and the bound stays where it was: a directory of 69 or 70 bytes takes the same branch as before.
func TestShortTempBaseAtTheEdgeOfTheBound(t *testing.T) {
	const localAppData = `C:\Users\runner\AppData\Local`
	const worstSocketPathBelow = len(`\pig\h4294967295\1z141z3.s`)
	if maxShortTempDirBytes != 70 || worstSocketPathBelow != 26 {
		t.Fatalf("the bound is %d and the longest socket path below a directory %d bytes, the rows name 70 and 26", maxShortTempDirBytes, worstSocketPathBelow)
	}
	for _, test := range []struct {
		dirBytes int
		kept     bool
	}{{68, true}, {69, true}, {70, true}, {71, false}} {
		temp := `D:\` + strings.Repeat("d", test.dirBytes-10-len(`D:\`)-len(`\pe`))
		if got := len(temp) + len(`\pe`) + 10; got != test.dirBytes {
			t.Fatalf("a temp directory of %d bytes makes a ShortTempDir of %d bytes, want %d", len(temp), got, test.dirBytes)
		}
		want := localAppData + `\pig\s`
		if test.kept {
			want = temp
			if limit := 107; test.dirBytes+worstSocketPathBelow > limit {
				t.Errorf("a kept directory of %d bytes holds a socket path of %d bytes, over the %d a sun_path allows", test.dirBytes, test.dirBytes+worstSocketPathBelow, limit)
			}
		}
		if got := shortTempBase("windows", temp, localAppData, "pe"); strings.ReplaceAll(got, "/", `\`) != want {
			t.Errorf("a ShortTempDir of %d bytes: shortTempBase = %q, want %q", test.dirBytes, got, want)
		}
	}
}
