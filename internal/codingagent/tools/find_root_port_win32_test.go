package tools

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/nodepath"
)

func TestFindRootRelativizationWindowsPort(t *testing.T) {
	// Upstream injects node:path's win32 module; so does relativizeFindResultPathFor, on every OS.
	for _, tc := range []struct{ name, result, root, want string }{
		// .upstream/v1.0.4/packages/coding-agent/test/suite/regressions/6104-find-root-relativization.test.ts:18
		{"preserves the first segment and emits one trailing slash for fd directory output", `I:\AI\Models\TextGen\gemma4\`, `I:\`, "AI/Models/TextGen/gemma4/"},
		// .upstream/v1.0.4/packages/coding-agent/test/suite/regressions/6104-find-root-relativization.test.ts:24
		{"handles fd output that uses forward slashes under a drive root", "I:/AI/Models/TextGen/gemma4/", `I:\`, "AI/Models/TextGen/gemma4/"},
		// .upstream/v1.0.4/packages/coding-agent/test/suite/regressions/6104-find-root-relativization.test.ts:30
		{"keeps deeper search paths unchanged", `I:\AI\Models\`, `I:\AI`, "Models/"},
		// .upstream/v1.0.4/packages/coding-agent/test/suite/regressions/6104-find-root-relativization.test.ts:34
		{"does not relativize a sibling directory that shares a name prefix", `I:\AI\Models2\file.txt`, `I:\AI\Models`, "../Models2/file.txt"},
		// .upstream/v1.0.4/packages/coding-agent/test/suite/regressions/6104-find-root-relativization.test.ts:40
		{"normalizes relative custom-glob results without corrupting them", `AI\Models\TextGen\gemma4\`, `I:\`, "AI/Models/TextGen/gemma4/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := relativizeFindResultPathFor(tc.result, tc.root, true, nodepath.Env{Cwd: `C:\work`}); got != tc.want {
				t.Fatalf("relative(%q,%q)=%q, want %q", tc.result, tc.root, got, tc.want)
			}
		})
	}
}
