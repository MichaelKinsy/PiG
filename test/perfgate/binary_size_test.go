// SPDX-License-Identifier: MIT

// The ceilings are measured on linux; other platforms allocate differently on their file, terminal and process paths.

//go:build linux

package perfgate

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/internal/perfgate"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// releasePlatforms are the stripped release builds whose size is gated; release-candidate.yml builds them with CGO_ENABLED=0, -trimpath and -ldflags "-s -w".
var releasePlatforms = []struct{ goos, goarch string }{
	{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "arm64"}, {"windows", "amd64"},
}

// The stock pig binary of each release platform, stripped as released, may only shrink.
func TestReleaseBinarySizeGate(t *testing.T) {
	root := testenv.ModuleRoot(t)
	for _, platform := range releasePlatforms {
		t.Run(platform.goos+"_"+platform.goarch, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "pig")
			build := exec.CommandContext(t.Context(), "go", "build", "-trimpath", "-buildvcs=false", "-ldflags", "-s -w", "-o", out, "./cmd/pig")
			build.Dir = root
			build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+platform.goos, "GOARCH="+platform.goarch)
			if data, err := build.CombinedOutput(); err != nil {
				t.Fatalf("go build: %v\n%s", err, data)
			}
			info, err := os.Stat(out)
			if err != nil {
				t.Fatal(err)
			}
			perfgate.Check(t, "binary.pig."+platform.goos+"_"+platform.goarch+".bytes", float64(info.Size()))
		})
	}
}

// The headless embed example (examples/headless: the session API, MCP and one provider, no terminal) of each release
// platform, stripped as released, may only shrink. "default" is a plain build, what an embedder gets without build tags.
// "stripped" passes every strip tag except the one that removes MCP, the set docs/site/docs/sdk.md recommends for an
// embedder that has no use for the features they name.
func TestHeadlessEmbedBinarySizeGate(t *testing.T) {
	root := testenv.ModuleRoot(t)
	for _, variant := range headlessVariants() {
		for _, platform := range releasePlatforms {
			t.Run(variant.name+"_"+platform.goos+"_"+platform.goarch, func(t *testing.T) {
				out := buildHeadless(t, root, variant.tags, platform.goos, platform.goarch)
				info, err := os.Stat(out)
				if err != nil {
					t.Fatal(err)
				}
				perfgate.Check(t, "binary.headless."+variant.name+"."+platform.goos+"_"+platform.goarch+".bytes", float64(info.Size()))
			})
		}
	}
}

type headlessVariant struct{ name, tags string }

// headlessVariants are the builds of the headless example that are gated: "default" and "stripped" (see TestHeadlessEmbedBinarySizeGate).
func headlessVariants() []headlessVariant {
	stripped := slices.DeleteFunc(piglet.StripBuildTags(), func(tag string) bool { return tag == "pig_strip_mcp" })
	return []headlessVariant{{"default", ""}, {"stripped", strings.Join(stripped, ",")}}
}

// buildHeadless builds examples/headless as released (CGO_ENABLED=0, -trimpath, -ldflags "-s -w") and returns the binary's path.
func buildHeadless(t *testing.T, root, tags, goos, goarch string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "headless")
	args := []string{"build", "-trimpath", "-buildvcs=false", "-ldflags", "-s -w", "-o", out}
	if tags != "" {
		args = append(args, "-tags", tags)
	}
	build := exec.CommandContext(t.Context(), "go", append(args, "./examples/headless")...)
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, data)
	}
	return out
}

// The word-segmentation dictionaries (internal/wordsegmenter/*_dictionary.bin, 6.3 MB) belong to the terminal editor and
// must not be linked into an embedder that draws no terminal. The size gate only notices when the program grows past its
// ceiling; this checks the content: the build fails if any 64-byte slice of a dictionary appears in the headless binary.
// (Linker deadcode keeps a go:embed blob only while the package's reader is reachable, so a partial copy cannot occur;
// the check covers every offset of the binary against every 64-byte window of each dictionary.)
func TestHeadlessEmbedLinksNoWordDictionaries(t *testing.T) {
	root := testenv.ModuleRoot(t)
	dictionaries, err := filepath.Glob(filepath.Join(root, "internal", "wordsegmenter", "*_dictionary.bin"))
	if err != nil || len(dictionaries) == 0 {
		t.Fatalf("no word dictionaries found under internal/wordsegmenter (err %v)", err)
	}
	for _, variant := range headlessVariants() {
		t.Run(variant.name, func(t *testing.T) {
			binary, err := os.ReadFile(buildHeadless(t, root, variant.tags, "linux", "amd64"))
			if err != nil {
				t.Fatal(err)
			}
			for _, dictionary := range dictionaries {
				data, err := os.ReadFile(dictionary)
				if err != nil {
					t.Fatal(err)
				}
				if offset, at := dictionarySliceIn(binary, data); at >= 0 {
					t.Errorf("%s: its bytes %d-%d appear in the headless binary at offset %d", filepath.Base(dictionary), at, at+dictionarySlice, offset)
				}
			}
		})
	}
}

// dictionarySlice is the length of the dictionary slices searched for.
const dictionarySlice = 64

// dictionarySliceIn finds the first offset of binary where a dictionarySlice-byte slice of dictionary starts, as the
// offset in binary and the offset in dictionary; at is -1 when none appears. Every offset of dictionary is a start: the
// slices are indexed by their first eight bytes.
func dictionarySliceIn(binary, dictionary []byte) (offset, at int) {
	index := make(map[uint64][]int, len(dictionary))
	for i := 0; i+dictionarySlice <= len(dictionary); i++ {
		key := binaryLE(dictionary[i:])
		index[key] = append(index[key], i)
	}
	for i := 0; i+dictionarySlice <= len(binary); i++ {
		for _, candidate := range index[binaryLE(binary[i:])] {
			if string(binary[i:i+dictionarySlice]) == string(dictionary[candidate:candidate+dictionarySlice]) {
				return i, candidate
			}
		}
	}
	return -1, -1
}

func binaryLE(b []byte) uint64 { return binary.LittleEndian.Uint64(b) }
