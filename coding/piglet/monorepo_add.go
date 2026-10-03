package piglet

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	sourceref "github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/linkerexec"
)

// pig additive (D18): a subdirectory add binds a clean selected Git tree to an exact commit, not a movable checkout or repository-root fallback.
func verifyPinnedPigletRoot(ref sourceref.Ref, root string) error {
	git := func(args ...string) (string, error) {
		output, err := linkerexec.Command("git", append([]string{"-C", root}, args...)...).Output()
		if err != nil {
			return "", fmt.Errorf("inspect pinned Piglet Git source: %w", err)
		}
		return strings.TrimSpace(string(output)), nil
	}
	commit, err := git("rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if commit != ref.GitRef {
		return fmt.Errorf("materialized Piglet commit %s does not match pinned commit %s", commit, ref.GitRef)
	}
	top, err := git("rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	if canonicalPath(root) != filepath.Join(canonicalPath(top), filepath.FromSlash(ref.GitSubdir)) {
		return fmt.Errorf("materialized Piglet root does not match selected Git subdirectory")
	}
	status, err := git("status", "--porcelain", "--untracked-files=all", "--", ".")
	if err != nil {
		return err
	}
	if status != "" {
		return fmt.Errorf("selected Piglet Git subdirectory has modified or untracked files")
	}
	return nil
}

// pig additive (D18): remote registration fails before installation when its owned closure exceeds bounded memory or entry limits.
const (
	maxPigletClosureEntries = 4096
	maxPigletClosureBytes   = 32 << 20
)

type pigletClosure struct {
	root    *os.Root
	prefix  string
	files   map[string][]byte
	modes   map[string]os.FileMode
	entries int
	size    int64
}

// pig additive (D18): copy only declared local Resource and prompt closures into a Piglet-owned namespace; never copy siblings or follow symlinks.
func planBundledPigletAdd(source resolvedPigletAddSource, p *Piglet, destination string) (pigletAddCandidate, error) {
	extensions, extensionErrors := ResolveExtensions(p)
	skills, skillErrors := ResolveSkills(p)
	if err := errors.Join(append(extensionErrors, skillErrors...)...); err != nil {
		return pigletAddCandidate{}, err
	}
	data, err := os.ReadFile(source.path)
	if err != nil {
		return pigletAddCandidate{}, err
	}
	origin, err := newPigletOrigin(source.originSource, source.materializedRoot, data, time.Now())
	if err != nil {
		return pigletAddCandidate{}, err
	}
	root, err := os.OpenRoot(filepath.Dir(source.path))
	if err != nil {
		return pigletAddCandidate{}, err
	}
	defer func() { _ = root.Close() }()
	closure := pigletClosure{root: root, prefix: p.Name + ".source/" + origin.Commit, files: map[string][]byte{}, modes: map[string]os.FileMode{}}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return pigletAddCandidate{}, err
	}
	if err := rejectBundledYAMLAliases(&document); err != nil {
		return pigletAddCandidate{}, err
	}
	if err := closure.rewrite(document.Content[0]); err != nil {
		return pigletAddCandidate{}, err
	}
	installedData, err := yaml.Marshal(&document)
	if err != nil {
		return pigletAddCandidate{}, err
	}
	if _, err := ParseBytes(installedData); err != nil {
		return pigletAddCandidate{}, err
	}
	origin.SourceDigest = digestPigletData(data)
	origin.PigletDigest = digestPigletData(installedData)
	origin.Files = map[string]string{}
	files := map[string][]byte{filepath.Join(destination, p.Name+".yaml"): installedData}
	modes := map[string]os.FileMode{}
	for relative, contents := range closure.files {
		files[filepath.Join(destination, filepath.FromSlash(relative))] = contents
		modes[filepath.Join(destination, filepath.FromSlash(relative))] = closure.modes[relative]
		origin.Files[relative] = digestPigletData(contents)
	}
	if ref, err := sourceref.Parse(source.originSource, sourceref.Options{Bare: sourceref.BareReject}); err != nil {
		return pigletAddCandidate{}, err
	} else if err := verifyPinnedPigletRoot(ref, source.materializedRoot); err != nil {
		return pigletAddCandidate{}, err
	}
	originData, err := marshalPigletOrigin(origin)
	if err != nil {
		return pigletAddCandidate{}, err
	}
	files[filepath.Join(destination, p.Name+".origin.json")] = originData
	return pigletAddCandidate{piglet: p, origin: &origin, extensionCount: len(extensions), skillCount: len(skills), files: files, modes: modes}, nil
}

func rejectBundledYAMLAliases(node *yaml.Node) error {
	if node.Kind == yaml.AliasNode || node.Tag == "!!merge" {
		return fmt.Errorf("bundled Piglet source requires explicit YAML fields, not aliases or merge keys")
	}
	for _, child := range node.Content {
		if err := rejectBundledYAMLAliases(child); err != nil {
			return err
		}
	}
	return nil
}

func (c *pigletClosure) rewrite(root *yaml.Node) error {
	rewriteLocal := func(node *yaml.Node) error {
		if node.Kind != yaml.ScalarNode {
			return fmt.Errorf("bundled Piglet sources must be scalar strings")
		}
		local, ok := strings.CutPrefix(node.Value, "local:")
		if !ok {
			return nil
		}
		relative, err := c.copy(local)
		if err != nil {
			return err
		}
		node.Value = "local:./" + relative
		return nil
	}
	for i := 0; i < len(root.Content); i += 2 {
		field, value := root.Content[i].Value, root.Content[i+1]
		switch field {
		case "packages":
			for j := 1; j < len(value.Content); j += 2 {
				if err := rewriteLocal(value.Content[j]); err != nil {
					return err
				}
			}
		case "extensions", "skills":
			for _, entry := range value.Content {
				for j := 0; j < len(entry.Content); j += 2 {
					if entry.Content[j].Value != "origins" {
						continue
					}
					for _, origin := range entry.Content[j+1].Content {
						if err := rewriteLocal(origin); err != nil {
							return err
						}
					}
				}
			}
		case "systemPrompt":
			for j := 0; j < len(value.Content); j += 2 {
				if value.Content[j].Value != "file" {
					continue
				}
				node := value.Content[j+1]
				relative, err := c.copy(node.Value)
				if err != nil {
					return err
				}
				node.Value = "./" + relative
			}
		}
	}
	return nil
}

func portableClosurePath(raw string) (string, error) {
	relative := strings.TrimPrefix(raw, "./")
	if relative == "." || !fs.ValidPath(relative) || strings.ContainsAny(relative, "\\:\x00") || strings.HasPrefix(relative, "~") {
		return "", fmt.Errorf("Piglet closure path must be a portable relative path")
	}
	for part := range strings.SplitSeq(relative, "/") {
		if part == ".git" {
			return "", fmt.Errorf("Piglet closure cannot include Git metadata")
		}
	}
	return relative, nil
}

func (c *pigletClosure) copy(raw string) (string, error) {
	relative, err := portableClosurePath(raw)
	if err != nil {
		return "", err
	}
	// Inspect ancestors before opening the selected path through the source root.
	path := ""
	for part := range strings.SplitSeq(relative, "/") {
		path = filepath.Join(path, part)
		info, err := c.root.Lstat(path)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("Piglet closure path %s is a symlink", path)
		}
	}
	return c.prefix + "/" + relative, c.collect(relative)
}

func (c *pigletClosure) collect(path string) error {
	if _, err := portableClosurePath(path); err != nil {
		return err
	}
	c.entries++
	if c.entries > maxPigletClosureEntries {
		return fmt.Errorf("Piglet closure exceeds entry limit")
	}
	info, err := c.root.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return fmt.Errorf("Piglet closure path %s is not a regular file or directory", path)
	}
	file, err := c.root.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	if info.IsDir() {
		for {
			entries, err := file.ReadDir(128)
			if err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			for _, entry := range entries {
				if err := c.collect(path + "/" + entry.Name()); err != nil {
					return err
				}
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
		}
	}
	info, err = file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("Piglet closure path %s is not a regular file", path)
	}
	target := c.prefix + "/" + path
	if _, exists := c.files[target]; exists {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(file, maxPigletClosureBytes-c.size+1))
	if err != nil {
		return err
	}
	c.size += int64(len(data))
	if c.size > maxPigletClosureBytes {
		return fmt.Errorf("Piglet closure exceeds size limit")
	}
	c.files[target] = data
	c.modes[target] = 0o644 | (info.Mode().Perm() & 0o111)
	return nil
}

func verifyPigletOriginFiles(path string, origin pigletOrigin) error {
	if len(origin.Files) == 0 {
		return nil
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if len(origin.Files) > maxPigletClosureEntries {
		return fmt.Errorf("Piglet origin closure exceeds entry limit")
	}
	var size int64
	prefix := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)) + ".source/" + origin.Commit + "/"
	for _, relative := range slices.Sorted(maps.Keys(origin.Files)) {
		if _, err := portableClosurePath(relative); err != nil || !strings.HasPrefix(relative, prefix) {
			return fmt.Errorf("Piglet origin closure path is outside its namespace")
		}
		if err := rejectPigletDestinationSymlinks(filepath.Join(filepath.Dir(path), filepath.FromSlash(relative))); err != nil {
			return err
		}
		file, err := root.Open(relative)
		if err != nil {
			return err
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			_ = file.Close()
			return fmt.Errorf("Piglet origin closure is not a regular file")
		}
		data, err := io.ReadAll(io.LimitReader(file, maxPigletClosureBytes-size+1))
		_ = file.Close()
		if err != nil {
			return err
		}
		size += int64(len(data))
		if size > maxPigletClosureBytes {
			return fmt.Errorf("Piglet origin closure exceeds size limit")
		}
		if digestPigletData(data) != origin.Files[relative] {
			return fmt.Errorf("Piglet origin closure file %s digest does not match", relative)
		}
	}
	return nil
}

func rejectPigletDestinationSymlinks(target string) error {
	root, err := filepath.Abs(codingagent.PigletsDir())
	if err != nil {
		return err
	}
	absolute, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil || !filepath.IsLocal(relative) {
		return fmt.Errorf("Piglet destination escapes the managed source root")
	}
	for path := absolute; ; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Piglet destination %s is a symlink", path)
		}
		if path == root {
			break
		}
	}
	return nil
}
