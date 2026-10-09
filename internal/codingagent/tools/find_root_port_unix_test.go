package tools

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/nodepath"
)

func TestFindRootRelativizationPOSIXPort(t *testing.T) {
	for _, tc := range []struct{ name, result, root, want string }{
		// .upstream/v1.0.4/packages/coding-agent/test/suite/regressions/6104-find-root-relativization.test.ts:48
		{"preserves the first segment for files under /", "/home/user/file.txt", "/", "home/user/file.txt"},
		// .upstream/v1.0.4/packages/coding-agent/test/suite/regressions/6104-find-root-relativization.test.ts:52
		{"preserves the first segment and one trailing slash for directories under /", "/home/user/project/", "/", "home/user/project/"},
		// .upstream/v1.0.4/packages/coding-agent/test/suite/regressions/6104-find-root-relativization.test.ts:56
		{"preserves backslashes in POSIX filenames", "/home/user/file\\", "/home/user", "file\\"},
		// .upstream/v1.0.4/packages/coding-agent/test/suite/regressions/6104-find-root-relativization.test.ts:62
		{"falls back to path.relative when the absolute paths do not share a prefix", "/tmp/results/file.txt", "/workspace/project", "../../tmp/results/file.txt"},
		// .upstream/v1.0.4/packages/coding-agent/test/suite/regressions/6104-find-root-relativization.test.ts:68
		{"keeps a trailing slash on directories resolved through path.relative", "/tmp/results/dir/", "/workspace/project", "../../tmp/results/dir/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := relativizeFindResultPathFor(tc.result, tc.root, false, nodepath.Env{Cwd: "/work"}); got != tc.want {
				t.Fatalf("relative(%q,%q)=%q, want %q", tc.result, tc.root, got, tc.want)
			}
		})
	}
}
