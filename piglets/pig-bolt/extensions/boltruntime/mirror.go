package boltruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// stageMirror is the directory of a stage that holds the extension files a rewritten build reads. It mirrors their absolute paths: /home/u/.pi/npm/node_modules/p/index.ts is
// <stage>/ext/home/u/.pi/npm/node_modules/p/index.ts. The bundler then resolves each import from the copy exactly as it did from the original, up the same chain of
// node_modules directories.
const stageMirror = "ext"

// maxMirroredEntries bounds how many entries of a directory that is not a node_modules directory the mirror links to the original. A directory above the extension's own
// (/tmp, /home) can hold far more than any resolution reaches, and past the bound only what the resolver looks for in a directory is linked.
const maxMirroredEntries = 8192

// resolverEntries are what the bundler looks for in a directory when it resolves an import from a file below it.
var resolverEntries = map[string]bool{"node_modules": true, "package.json": true, "tsconfig.json": true, "jsconfig.json": true}

// mirror is the copy of the extension files that a rewritten build reads: each original file by its path in the stage.
type mirror struct {
	root   string
	copyOf map[string]string
	origin map[string]string
}

// place is where the copy of the file at original goes. A volume name becomes the first directory.
func (m *mirror) place(original string) string {
	volume := filepath.VolumeName(original)
	rest := original[len(volume):]
	if volume != "" {
		volume = strings.NewReplacer(":", "", `\`, "_", "/", "_").Replace(volume)
	}
	return filepath.Join(m.root, volume, rest)
}

// writeMirror lays the extension files out in the stage as the build from copies reads them. Each file of inputs, the files the first build bundled, is a file of its own:
// its source from rewrites where it has one, else a link to the original where the filesystem allows it, else a copy. Every directory on the way to such a file is a
// directory of its own too, and each other entry in it is a link to the original. The bundler resolves an import by looking at what is beside the importing file and in the
// node_modules above it, and it looks at files it then leaves out of the bundle (a re-export that nothing uses, a module only an optional path imports): the links
// answer those lookups as the originals would, and the files that are bundled are the copies, whose own imports stay inside the mirror.
func (s stage) writeMirror(inputs []string, rewrites map[string]string) (*mirror, error) {
	m := &mirror{root: filepath.Join(s.dir, stageMirror), copyOf: map[string]string{}, origin: map[string]string{}}
	dirs := map[string]bool{}
	for _, input := range inputs {
		for dir := filepath.Dir(input); !dirs[dir]; dir = filepath.Dir(dir) {
			dirs[dir] = true
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	placed := map[string]bool{}
	for _, input := range inputs {
		placed[input] = true
	}
	for _, dir := range slices.Sorted(func(yield func(string) bool) {
		for d := range dirs {
			if !yield(d) {
				return
			}
		}
	}) {
		if err := os.MkdirAll(m.place(dir), 0o755); err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", dir, err)
		}
		full := len(entries) <= maxMirroredEntries || filepath.Base(dir) == "node_modules"
		for _, entry := range entries {
			child := filepath.Join(dir, entry.Name())
			if dirs[child] || placed[child] || !full && !resolverEntries[entry.Name()] {
				continue
			}
			if err := os.Symlink(child, m.place(child)); err != nil {
				return nil, fmt.Errorf("link %s: %w", child, err)
			}
		}
	}
	for _, input := range inputs {
		dst := m.place(input)
		if source, ok := rewrites[input]; ok {
			// A new file: the copy of a changed file is never a link to the original.
			if err := os.WriteFile(dst, []byte(source), 0o644); err != nil {
				return nil, fmt.Errorf("copy %s: %w", input, err)
			}
		} else if os.Link(input, dst) != nil {
			if err := copyFile(input, dst); err != nil {
				return nil, fmt.Errorf("copy %s: %w", input, err)
			}
		}
		m.copyOf[input] = dst
		m.origin[dst] = input
	}
	return m, nil
}

// pathOf is the copy of a member's entry file. The bundler names a file by its real path, so a member reached through a link is found by the link's target.
func (m *mirror) pathOf(entry string) (string, error) {
	if dst, ok := m.copyOf[entry]; ok {
		return dst, nil
	}
	if real, err := filepath.EvalSymlinks(entry); err == nil {
		if dst, ok := m.copyOf[real]; ok {
			return dst, nil
		}
	}
	return "", fmt.Errorf("the entry %s is not among the files the first build read", entry)
}

// originalOf is the file a path of the build from copies stands for: the original of a copy, the target of a link of the mirror, or path itself.
func (m *mirror) originalOf(path string) string {
	if original, ok := m.origin[path]; ok {
		return original
	}
	if strings.HasPrefix(path, m.root+string(filepath.Separator)) {
		if target, err := filepath.EvalSymlinks(path); err == nil {
			return target
		}
	}
	return path
}

// writeFactories writes the module that registers the compiled-in extensions' factories, each imported from specifier(entry) and keyed by its entry, the path PiG
// names the extension by.
func (s stage) writeFactories(members []Member, specifier func(entry string) (string, error)) error {
	var factories strings.Builder
	factories.WriteString("// Generated by the pig-bolt Piglet. Do not edit.\n")
	for i, member := range members {
		path, err := specifier(member.Entry)
		if err != nil {
			return err
		}
		fmt.Fprintf(&factories, "import * as m%d from %s;\n", i, strconv.Quote(path))
	}
	factories.WriteString(factoryHelpers)
	factories.WriteString("globalThis[Symbol.for(\"pig.compiledFactories\")] = new Map([\n")
	for i, member := range members {
		fmt.Fprintf(&factories, "  [%s, factoryOf(m%d)],\n", strconv.Quote(member.Entry), i)
	}
	factories.WriteString("]);\n")
	return os.WriteFile(filepath.Join(s.dir, "aot-factories.mjs"), []byte(factories.String()), 0o644)
}

// bundleRewritten builds the bytecode host again, from copies of the extension files in which each expression that names a file's location says where the original is
// (plan.Rewrites). The staleness inputs stay the original files: the image is built from copies of them, and a change to an original marks the image stale. The two
// builds must read the same files; one that reads another means the copies resolve differently from the originals, and the build is refused.
func (s stage) bundleRewritten(ctx context.Context, opts buildOptions, config, bytecodeHost string, inputs []inputFile, plan rewritePlan, log io.Writer) error {
	files := make([]string, len(inputs))
	for i, input := range inputs {
		files[i] = input.Path
	}
	m, err := s.writeMirror(files, plan.Rewrites)
	if err != nil {
		return err
	}
	if err := s.writeFactories(opts.Request.Members, m.pathOf); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(log, "rewrite: %d of %d extension files name their location; building from copies\n", len(plan.Rewrites), len(files))
	meta := filepath.Join(opts.WorkDir, "meta-rewritten.json")
	if err := runCompiler(ctx, opts, s.dir, config, meta, "", bytecodeHost, nil, log); err != nil {
		return err
	}
	read, err := metafileInputs(meta, s.dir, filepath.Join(s.dir, stageTree), opts.Request.Members)
	if err != nil {
		return fmt.Errorf("read the build's input list: %w", err)
	}
	var got []string
	for _, input := range read {
		got = append(got, m.originalOf(input.Path))
	}
	slices.Sort(got)
	got = slices.Compact(got)
	var want []string
	for _, input := range inputs {
		want = append(want, input.Path)
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		return errors.New("the build from copies read other files than the build from the originals: " + differenceOf(got, want))
	}
	return nil
}

// differenceOf names the first paths that only one of two sorted lists holds.
func differenceOf(got, want []string) string {
	var extra, missing []string
	for _, p := range got {
		if _, found := slices.BinarySearch(want, p); !found {
			extra = append(extra, p)
		}
	}
	for _, p := range want {
		if _, found := slices.BinarySearch(got, p); !found {
			missing = append(missing, p)
		}
	}
	return fmt.Sprintf("read %v, did not read %v", extra[:min(len(extra), 3)], missing[:min(len(missing), 3)])
}
