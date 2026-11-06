package piglet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// pig additive (D18): a local or non-bundled source registration records the files `piglet add` copied for it (path and
// content digest), so `piglet remove --source` and a re-add of the same name delete or replace exactly those files and
// never a shared, edited, or unrelated one. The record lives beside the registered YAML as <name>.files.json.
type pigletCopies struct {
	Files map[string]string `json:"files"`
}

func copiesPathForPiglet(path string) string {
	return strings.TrimSuffix(path, filepath.Ext(path)) + ".files.json"
}

func (c pigletCopies) validate() error {
	if len(c.Files) == 0 {
		return fmt.Errorf("Piglet copied-file record lists no files")
	}
	for relative, digest := range c.Files {
		if err := validCopiedPath(relative); err != nil || !validSHA256Digest(digest) {
			return fmt.Errorf("invalid Piglet copied-file entry %q", relative)
		}
	}
	return nil
}

// validCopiedPath accepts a clean slash-separated path that stays inside the Piglet directory on this platform. It
// allows every name a local prompt file could have, including ones a Git or npm closure refuses, but never one that
// this platform would read as a parent, a drive, or a different separator.
func validCopiedPath(relative string) error {
	if relative == "." || !fs.ValidPath(relative) || strings.ContainsRune(relative, 0) || !filepath.IsLocal(filepath.FromSlash(relative)) || filepath.ToSlash(filepath.FromSlash(relative)) != relative {
		return fmt.Errorf("%q is not a path inside the Piglet directory that this system can create", relative)
	}
	return nil
}

func marshalPigletCopies(copies pigletCopies) ([]byte, error) {
	data, err := json.MarshalIndent(copies, "", "  ") //portlint:allow jsonescape the record is PiG's own file, written and read back by PiG's Go json, which decodes \u003c and < alike; Pi has no such file
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// readPigletCopies returns nil when the registration copied no files, or predates the record.
func readPigletCopies(path string) (*pigletCopies, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Piglet copied-file record %s: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var copies pigletCopies
	if err := decoder.Decode(&copies); err != nil {
		return nil, fmt.Errorf("Piglet copied-file record %s: %w", path, err)
	}
	if err := rejectTrailingOriginJSON(decoder); err != nil {
		return nil, fmt.Errorf("Piglet copied-file record %s: %w", path, err)
	}
	if err := copies.validate(); err != nil {
		return nil, fmt.Errorf("Piglet copied-file record %s: %w", path, err)
	}
	return &copies, nil
}

// sameFilePath reports whether two paths name one file. It asks the file system when both exist, which settles case
// folding, Unicode normalization and links on every platform, and otherwise compares the cleaned spellings.
func sameFilePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	infoA, errA := os.Stat(a) //perf:ok cold: a piglet add or remove compares a few paths
	infoB, errB := os.Stat(b) //perf:ok cold: a piglet add or remove compares a few paths
	return errA == nil && errB == nil && os.SameFile(infoA, infoB)
}

type pigletFileClaim struct {
	path string
	name string
}

// pigletFileClaims lists the files other registered source Piglets own or reference.
type pigletFileClaims []pigletFileClaim

// owners returns the names of the Piglets that claim target.
func (c pigletFileClaims) owners(target string) []string {
	var names []string
	for _, claim := range c {
		if !slices.Contains(names, claim.name) && sameFilePath(claim.path, target) {
			names = append(names, claim.name)
		}
	}
	return names
}

// otherRegistrationClaims collects each file a registered source Piglet other than exceptYAML owns (its copied-file
// record) or references (a relative systemPrompt file, which covers registrations that predate the record).
func otherRegistrationClaims(pigletsDir, exceptYAML string) (pigletFileClaims, error) {
	entries, err := os.ReadDir(pigletsDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Piglet directory %s: %w", pigletsDir, err)
	}
	var claims pigletFileClaims
	for _, entry := range entries {
		ext := filepath.Ext(entry.Name())
		if entry.IsDir() || (ext != ".yaml" && ext != ".yml") {
			continue
		}
		path := filepath.Join(pigletsDir, entry.Name())
		if exceptYAML != "" && sameFilePath(path, exceptYAML) {
			continue
		}
		other, err := Parse(path)
		if err != nil {
			return nil, fmt.Errorf("cannot tell whether %s shares copied files: %w", path, err)
		}
		if other.SystemPrompt != nil && other.SystemPrompt.File != "" && !filepath.IsAbs(other.SystemPrompt.File) {
			claims = append(claims, pigletFileClaim{filepath.Join(pigletsDir, filepath.FromSlash(other.SystemPrompt.File)), other.Name})
		}
		copies, err := readPigletCopies(copiesPathForPiglet(path))
		if err != nil {
			return nil, err
		}
		if copies != nil {
			for relative := range copies.Files {
				claims = append(claims, pigletFileClaim{filepath.Join(pigletsDir, filepath.FromSlash(relative)), other.Name})
			}
		}
	}
	return claims, nil
}

// pigletCopiedFilePlan is what removing one source registration does with the files it copied.
type pigletCopiedFilePlan struct {
	removals []pigletFacetRemoval
	kept     []string
}

// planPigletCopiedFiles lists the files the registration at yamlPath copied and may still delete: each one that no other
// registration owns or references, and whose content still matches the digest recorded when it was copied. Every other
// recorded file stays, with the reason reported. The copied-file record itself is always removed.
func planPigletCopiedFiles(yamlPath string) (pigletCopiedFilePlan, error) {
	var plan pigletCopiedFilePlan
	recordPath := copiesPathForPiglet(yamlPath)
	copies, err := readPigletCopies(recordPath)
	if err != nil || copies == nil {
		return plan, err
	}
	dir := filepath.Dir(yamlPath)
	claims, err := otherRegistrationClaims(dir, yamlPath)
	if err != nil {
		return plan, err
	}
	for _, relative := range slices.Sorted(maps.Keys(copies.Files)) {
		target := filepath.Join(dir, filepath.FromSlash(relative))
		if sameFilePath(target, yamlPath) || sameFilePath(target, recordPath) || sameFilePath(target, originPathForPiglet(yamlPath)) {
			continue
		}
		if others := claims.owners(target); len(others) > 0 {
			plan.kept = append(plan.kept, fmt.Sprintf("kept %s: Piglet %s also uses it", target, strings.Join(others, ", "))) //perf:ok cold: a message per kept file of one piglet remove
			continue
		}
		info, err := os.Lstat(target) //perf:ok cold: once per file a piglet copied, on remove
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return plan, fmt.Errorf("inspect copied file %s: %w", target, err)
		}
		if !info.Mode().IsRegular() || rejectPigletDestinationSymlinks(target) != nil {
			plan.kept = append(plan.kept, fmt.Sprintf("kept %s: it is no longer a regular file PiG copied", target)) //perf:ok cold: a message per kept file of one piglet remove
			continue
		}
		data, err := os.ReadFile(target)
		if err != nil {
			return plan, fmt.Errorf("read copied file %s: %w", target, err)
		}
		if digestPigletData(data) != copies.Files[relative] {
			plan.kept = append(plan.kept, fmt.Sprintf("kept %s: it changed after PiG copied it; delete it yourself if you no longer need it", target)) //perf:ok cold: a message per kept file of one piglet remove
			continue
		}
		plan.removals = append(plan.removals, pigletFacetRemoval{path: target, label: "copied file"})
	}
	plan.removals = append(plan.removals, pigletFacetRemoval{path: recordPath, label: "copied-file record"})
	return plan, nil
}

// reportPigletRemovals prints each removed facet and each copied file kept, and deletes directories a removed copy left
// empty, up to the Piglet directory.
func reportPigletRemovals(root string, removals []pigletFacetRemoval, kept []string, stdout io.Writer) {
	for _, removal := range removals {
		_, _ = fmt.Fprintf(stdout, "removed %s: %s\n", removal.label, removal.path)
		if removal.label == "copied file" {
			pruneEmptyParents(root, removal.path)
		}
	}
	for _, note := range kept {
		_, _ = fmt.Fprintf(stdout, "%s\n", note)
	}
}

func pruneEmptyParents(root, path string) {
	for dir := filepath.Dir(path); dir != root && filepath.IsLocal(relativeTo(root, dir)); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			return
		}
	}
}

func relativeTo(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return ".."
	}
	return relative
}

// explainPigletDestinationConflict names the safe next step when add finds a different file at a destination.
func explainPigletDestinationConflict(pigletsDir, target string) string {
	if name, ok := pigletRegistrationName(pigletsDir, target); ok {
		return fmt.Sprintf("Piglet %q is already registered; run `pig piglet remove %s --source` before adding a replacement (this also deletes the files PiG copied for it)", name, name)
	}
	claims, err := otherRegistrationClaims(pigletsDir, "")
	if err == nil {
		if owners := claims.owners(target); len(owners) > 0 {
			return fmt.Sprintf("Piglet %s uses it; run `pig piglet remove %s --source` first, or give this Piglet a different prompt path", strings.Join(owners, ", "), owners[0])
		}
	}
	return "no registered Piglet copied it, so PiG will not delete it; if it is a leftover, delete that file yourself and add again, or give this Piglet a different prompt path"
}

// pigletRegistrationName returns the Piglet a file directly in pigletsDir belongs to as its registered YAML, or as an
// origin or copied-file record while that YAML exists. A record whose YAML is gone belongs to no registered Piglet, so
// `piglet remove` has nothing to remove for it.
func pigletRegistrationName(pigletsDir, target string) (string, bool) {
	if filepath.Dir(target) != pigletsDir {
		return "", false
	}
	base := filepath.Base(target)
	for _, suffix := range []string{".yaml", ".yml"} {
		if name, ok := strings.CutSuffix(base, suffix); ok && name != "" {
			return name, true
		}
	}
	for _, suffix := range []string{".files.json", ".origin.json"} {
		if name, ok := strings.CutSuffix(base, suffix); ok && name != "" {
			for _, ext := range []string{".yaml", ".yml"} {
				if _, err := os.Stat(filepath.Join(pigletsDir, name+ext)); err == nil { //perf:ok cold: once per origin record on a piglet add
					return name, true
				}
			}
		}
	}
	return "", false
}

// reclaimOrphanedPigletCopies lets add replace the files an earlier registration of the same name copied when its YAML is
// already gone (for example deleted by hand): the earlier record still names them, and the same ownership and digest
// rules as `piglet remove --source` decide which are deleted.
func reclaimOrphanedPigletCopies(pigletsDir, yamlPath string, stdout io.Writer) error {
	if _, err := os.Stat(yamlPath); !os.IsNotExist(err) { //perf:ok cold: once per piglet add
		return err
	}
	plan, err := planPigletCopiedFiles(yamlPath)
	if err != nil || len(plan.removals) == 0 {
		return err
	}
	if err := removePigletFacets(plan.removals); err != nil {
		return err
	}
	reportPigletRemovals(pigletsDir, plan.removals, plan.kept, stdout)
	return nil
}

func fileExists(path string) bool {
	_, err := os.Lstat(path) //perf:ok cold: a piglet add or remove
	return err == nil
}
