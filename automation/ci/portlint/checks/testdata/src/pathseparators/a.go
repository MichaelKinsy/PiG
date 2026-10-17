package pathseparators

import (
	"os"
	"path"
	"strings"
)

func bad(p string) []string {
	_, _ = os.Stat(path.Join(p, "x")) // want `path.Join on a file path splits on / only`
	data, _ := os.ReadFile(p)
	return strings.Split(string(data), "\n") // want `splitting on \\n leaves a trailing`
}
