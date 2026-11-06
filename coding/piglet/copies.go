package piglet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
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
		if clean, err := portableClosurePath(relative); err != nil || clean != relative || !validSHA256Digest(digest) {
			return fmt.Errorf("invalid Piglet copied-file entry %q", relative)
		}
	}
	return nil
}

func marshalPigletCopies(copies pigletCopies) ([]byte, error) {
	data, err := json.MarshalIndent(copies, "", "  ")
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

// claimKey compares paths the way the strictest supported file system does, so a case-only difference never lets one
// registration's file look unclaimed.
func claimKey(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.ToLower(path)
	}
	return path
}

// otherRegistrationClaims maps each file a registered source Piglet other than exceptYAML owns (its copied-file record)
// or references (a relative systemPrompt file, which covers registrations that predate the record) to the names of
// those Piglets.
func otherRegistrationClaims(pigletsDir, exceptYAML string) (map[string][]string, error) {
	entries, err := os.ReadDir(pigletsDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Piglet directory %s: %w", pigletsDir, err)
	}
	claims := map[string][]string{}
	claim := func(target, name string) {
		key := claimKey(target)
		if !slices.Contains(claims[key], name) {
			claims[key] = append(claims[key], name)
		}
	}
	for _, entry := range entries {
		ext := filepath.Ext(entry.Name())
		if entry.IsDir() || (ext != ".yaml" && ext != ".yml") {
			continue
		}
		path := filepath.Join(pigletsDir, entry.Name())
		if claimKey(path) == claimKey(exceptYAML) {
			continue
		}
		other, err := Parse(path)
		if err != nil {
			return nil, fmt.Errorf("cannot tell whether %s shares copied files: %w", path, err)
		}
		if other.SystemPrompt != nil && other.SystemPrompt.File != "" && !filepath.IsAbs(other.SystemPrompt.File) {
			claim(filepath.Join(pigletsDir, filepath.FromSlash(other.SystemPrompt.File)), other.Name)
		}
		copies, err := readPigletCopies(copiesPathForPiglet(path))
		if err != nil {
			return nil, err
		}
		if copies != nil {
			for relative := range copies.Files {
				claim(filepath.Join(pigletsDir, filepath.FromSlash(relative)), other.Name)
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
		if others := claims[claimKey(target)]; len(others) > 0 {
			plan.kept = append(plan.kept, fmt.Sprintf("kept %s: Piglet %s also uses it", target, strings.Join(others, ", ")))
			continue
		}
		info, err := os.Lstat(target)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return plan, fmt.Errorf("inspect copied file %s: %w", target, err)
		}
		if !info.Mode().IsRegular() || rejectPigletDestinationSymlinks(target) != nil {
			plan.kept = append(plan.kept, fmt.Sprintf("kept %s: it is no longer a regular file PiG copied", target))
			continue
		}
		data, err := os.ReadFile(target)
		if err != nil {
			return plan, fmt.Errorf("read copied file %s: %w", target, err)
		}
		if digestPigletData(data) != copies.Files[relative] {
			plan.kept = append(plan.kept, fmt.Sprintf("kept %s: it changed after PiG copied it; delete it yourself if you no longer need it", target))
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
	if ext := filepath.Ext(target); filepath.Dir(target) == pigletsDir && (ext == ".yaml" || ext == ".yml") {
		name := strings.TrimSuffix(filepath.Base(target), ext)
		return fmt.Sprintf("Piglet %q is already registered; run `pig piglet remove %s --source` before adding a replacement (this also deletes the files PiG copied for it)", name, name)
	}
	claims, err := otherRegistrationClaims(pigletsDir, "")
	if err == nil {
		if owners := claims[claimKey(target)]; len(owners) > 0 {
			return fmt.Sprintf("Piglet %s uses it; run `pig piglet remove %s --source` first, or give this Piglet a different prompt path", strings.Join(owners, ", "), owners[0])
		}
	}
	return "no registered Piglet copied it, so PiG will not delete it; if it is a leftover, delete that file yourself and add again, or give this Piglet a different prompt path"
}

// reclaimOrphanedPigletCopies lets add replace the files an earlier registration of the same name copied when its YAML is
// already gone (for example deleted by hand): the earlier record still names them, and the same ownership and digest
// rules as `piglet remove --source` decide which are deleted.
func reclaimOrphanedPigletCopies(pigletsDir, yamlPath string, stdout io.Writer) error {
	if _, err := os.Stat(yamlPath); !os.IsNotExist(err) {
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
