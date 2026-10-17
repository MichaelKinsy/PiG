package aotruntime

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// A compiled-in extension runs from one executable: its files are not on disk beside it, so code that reads a file by its own location finds nothing
// where Node and Bun find it. scanPathSensitive finds that code so that the build refuses it instead of changing the extension's behavior.

// selfLocation matches how a module names its own location.
var selfLocation = regexp.MustCompile(`__dirname|__filename|import\.meta\.(url|dir|dirname|filename|path|resolve)|require\.resolve\b`)

// fileAccess matches a read of the file system, or a start of a process or worker by path, which a module-relative location feeds.
var fileAccess = regexp.MustCompile(`\b(readFileSync|readFile|readdirSync|readdir|existsSync|statSync|lstatSync|accessSync|createReadStream|openSync|opendirSync|cpSync|spawn|spawnSync|execFile|execFileSync|fork)\s*\(|new\s+Worker\s*\(|\bBun\.file\s*\(`)

const maxScanSize = 4 << 20

// pathFinding is a file that locates itself and uses the file system.
type pathFinding struct {
	File   string
	Marker string
}

func (f pathFinding) String() string {
	return fmt.Sprintf("%s locates itself by %s and reads files", f.File, f.Marker)
}

// scanPathSensitive returns the first file among files that both names its own location and accesses the file system, or false. A file larger than
// the scan limit is read up to the limit.
func scanPathSensitive(files []string) (pathFinding, bool, error) {
	for _, file := range files {
		switch {
		case strings.HasSuffix(file, ".json"), strings.HasSuffix(file, ".node"), strings.HasSuffix(file, ".wasm"), strings.HasSuffix(file, ".d.ts"):
			continue
		}
		f, err := os.Open(file)
		if err != nil {
			return pathFinding{}, false, err
		}
		data, readErr := io.ReadAll(io.LimitReader(f, maxScanSize))
		_ = f.Close()
		if readErr != nil {
			return pathFinding{}, false, readErr
		}
		if marker := selfLocation.Find(data); marker != nil && fileAccess.Match(data) {
			return pathFinding{File: file, Marker: string(marker)}, true, nil
		}
	}
	return pathFinding{}, false, nil
}
