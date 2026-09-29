//go:build windows

package nodespawn

import (
	"slices"
	"testing"
)

// The candidates are the paths libuv's search_path (src/win/process.c) tests
// with GetFileAttributesW, in order, when none is a file: the cwd walk for a
// bare name, then each PATH slice; the exact name only when it has an
// extension, then .com and .exe appended; cwd joined by
// search_path_join_test's UNC, rooted, drive-relative, and absolute rules.
func TestSearchPathTriesLibuvCandidatesInOrder(t *testing.T) {
	cases := []struct {
		name      string
		file      string
		cwd       string
		path      string
		searchCwd bool
		want      []string
	}{
		{"bare name: cwd, then PATH", "tool", `C:\work`, `C:\a;C:\b\`, true, []string{
			`C:\work\tool.com`, `C:\work\tool.exe`, `C:\a\tool.com`, `C:\a\tool.exe`, `C:\b\tool.com`, `C:\b\tool.exe`,
		}},
		{"NoDefaultCurrentDirectoryInExePath skips cwd", "tool", `C:\work`, `C:\a`, false, []string{`C:\a\tool.com`, `C:\a\tool.exe`}},
		{"an extension is tried as given, then appended to", "npm.cmd", `C:\work`, `C:\a`, true, []string{
			`C:\work\npm.cmd`, `C:\work\npm.cmd.com`, `C:\work\npm.cmd.exe`, `C:\a\npm.cmd`, `C:\a\npm.cmd.com`, `C:\a\npm.cmd.exe`,
		}},
		{"a final dot is no extension", "tool.", `C:\work`, "", true, []string{`C:\work\tool.com`, `C:\work\tool.exe`}},
		{"a dot in the directory is no extension", `a.b\tool`, `C:\work`, "", true, []string{`C:\work\a.b\tool.com`, `C:\work\a.b\tool.exe`}},
		{"a name with a dot is tried as given", "my.tool", `C:\work`, "", true, []string{`C:\work\my.tool`, `C:\work\my.tool.com`, `C:\work\my.tool.exe`}},
		{"a relative directory skips PATH", `sub\rel`, `C:\work`, `C:\a`, false, []string{`C:\work\sub\rel.com`, `C:\work\sub\rel.exe`}},
		{"a forward slash is a directory separator", "sub/rel", `C:\work`, `C:\a`, true, []string{`C:\work\sub/rel.com`, `C:\work\sub/rel.exe`}},
		{"dot directory", `.\tool`, `C:\work`, "", true, []string{`C:\work\.\tool.com`, `C:\work\.\tool.exe`}},
		{"absolute path ignores cwd", `D:\x\tool`, `C:\work`, `C:\a`, true, []string{`D:\x\tool.com`, `D:\x\tool.exe`}},
		{"rooted path takes cwd's drive", `\x\tool`, `C:\work`, "", true, []string{`C:\x\tool.com`, `C:\x\tool.exe`}},
		{"rooted path after a cwd shorter than a drive", `\x\tool`, "a", "", true, []string{"a", "a"}},
		{"UNC path ignores cwd", `\\srv\share\tool`, `C:\work`, "", true, []string{`\\srv\share\tool.com`, `\\srv\share\tool.exe`}},
		{"drive-relative path on cwd's drive", "c:tool", `C:\work`, "", true, []string{`C:\work\tool.com`, `C:\work\tool.exe`}},
		{"drive-relative directory on cwd's drive", `C:sub\tool`, `C:\work`, "", true, []string{`C:\work\sub\tool.com`, `C:\work\sub\tool.exe`}},
		{"drive-relative path on another drive", "D:tool", `C:\work`, "", true, []string{`D:tool.com`, `D:tool.exe`}},
		{"a bare drive has an empty name", "C:", `C:\work`, "", true, []string{`C:\work\com`, `C:\work\exe`}},
		{"cwd ending in a separator", "tool", `C:\`, "", true, []string{`C:\tool.com`, `C:\tool.exe`}},
		{"quoted, empty, and trailing-separator PATH entries", "tool", `C:\work`, `"C:\a;b";'C:\c';;C:\d\;"C:\e`, false, []string{
			`C:\a;b\tool.com`, `C:\a;b\tool.exe`, `C:\c\tool.com`, `C:\c\tool.exe`, `C:\d\tool.com`, `C:\d\tool.exe`, `C:\e\tool.com`, `C:\e\tool.exe`,
		}},
		{"leading PATH separator", "tool", `C:\work`, `;C:\a`, false, []string{`C:\a\tool.com`, `C:\a\tool.exe`}},
		{"an empty quoted PATH entry is cwd", "tool", `C:\work`, `"";C:\a`, false, []string{`C:\work\tool.com`, `C:\work\tool.exe`, `C:\a\tool.com`, `C:\a\tool.exe`}},
		{"an unclosed quote takes the rest of PATH", "tool", `C:\work`, `";C:\a`, false, []string{`C:\work\;C:\a\tool.com`, `C:\work\;C:\a\tool.exe`}},
		{"a lone quote entry is skipped", "tool", `C:\work`, `C:\a;"`, false, []string{`C:\a\tool.com`, `C:\a\tool.exe`}},
		{"empty file", "", `C:\work`, `C:\a`, true, nil},
		{"dot file", ".", `C:\work`, `C:\a`, true, nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var tried []string
			got := searchPath(testCase.file, testCase.cwd, testCase.path, testCase.searchCwd, func(candidate string) bool {
				tried = append(tried, candidate)
				return false
			})
			if got != "" || !slices.Equal(tried, testCase.want) {
				t.Fatalf("searchPath(%q) = %q after\n %q\nwant \"\" after\n %q", testCase.file, got, tried, testCase.want)
			}
		})
	}
}

// search_path returns the first candidate that is a file: .com before .exe,
// cwd before PATH, and an earlier PATH entry before a later one.
func TestSearchPathReturnsTheFirstFile(t *testing.T) {
	cases := []struct {
		files []string
		want  string
	}{
		{[]string{`C:\work\tool.exe`, `C:\work\tool.com`}, `C:\work\tool.com`},
		{[]string{`C:\a\tool.exe`, `C:\work\tool.exe`}, `C:\work\tool.exe`},
		{[]string{`C:\b\tool.com`, `C:\a\tool.exe`}, `C:\a\tool.exe`},
		{[]string{`C:\a\tool.cmd`, `C:\a\tool.bat`}, ""},
	}
	for _, testCase := range cases {
		got := searchPath("tool", `C:\work`, `C:\a;C:\b`, true, func(candidate string) bool {
			return slices.Contains(testCase.files, candidate)
		})
		if got != testCase.want {
			t.Errorf("files %q: searchPath = %q, want %q", testCase.files, got, testCase.want)
		}
	}
}

// Node 22.19 and 24.19 throw EINVAL from spawn for a file whose last
// extension, ignoring trailing spaces and dots, is cmd or bat
// (IsWindowsBatchFile in src/util-inl.h). The cases were observed from Node
// 24.19 on Windows: MSVC's std::regex matches \s against C-locale spaces,
// including \v and \f, and $ before a line feed but not a carriage return.
func TestIsWindowsBatchFileMatchesNode(t *testing.T) {
	batch := []string{
		"tool.cmd", "tool.bat", "tool.Cmd", "x.CmD", ".cmd", "noext.bat", "x.exe.bat", `C:\dir\tool.cmd`,
		"tool.cmd.", "tool.cmd ", "tool.CMD\t", "x.cmd.\v.", "x.cmd\v", "x.cmd\f", "x.cmd\r",
		"x.cmd\n", "x.cmd\nfoo", "x.cmd\nfoo\nbar", "x.cmd\n.exe", "x.cmd\r\nfoo", "x.bat.\n.", "x.cmd \nfoo", "x.cmd.\nfoo",
	}
	notBatch := []string{
		"tool", "npm", "cmd", "x.bat.exe", `dir.cmd\tool`, "x.c\u00e9md", "x.cmd\u00a0", "x.cmd\u3000", "x.cmd\u0085",
		"x.cmd\u2028foo", "x.bat\u2028", "x.cmd\rfoo", "x.exe\n.cmd\nz", "x.cmd\vfoo", "x.cmd\ffoo", "x.cmd\tfoo",
	}
	for _, file := range batch {
		if !isWindowsBatchFile(file) {
			t.Errorf("isWindowsBatchFile(%q) = false, want true", file)
		}
	}
	for _, file := range notBatch {
		if isWindowsBatchFile(file) {
			t.Errorf("isWindowsBatchFile(%q) = true, want false", file)
		}
	}
}
