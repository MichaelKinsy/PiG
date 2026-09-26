package codingagent

import (
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

// compactResourceFileNames is upstream read.ts COMPACT_RESOURCE_FILE_NAMES.
var compactResourceFileNames = map[string]bool{"AGENTS.override.md": true, "AGENTS.md": true, "AGENTS.MD": true, "CLAUDE.md": true, "CLAUDE.MD": true}

func init() {
	tui.SetCompactReadClassifier(classifyCompactRead)
}

// classifyCompactRead is upstream read.ts getCompactReadClassification: a
// SKILL.md reads as "[skill] <directory>", the agent's own documentation as
// "read docs <path>", and a context file as "read resource <path>".
func classifyCompactRead(rawPath, cwd string) (tui.CompactReadClassification, bool) {
	absolutePath := tools.ResolveToCwd(rawPath, cwd)
	fileName := filepath.Base(absolutePath)
	if fileName == "SKILL.md" {
		label := filepath.Base(filepath.Dir(absolutePath))
		if label == "" || label == "." || label == string(filepath.Separator) {
			label = fileName
		}
		return tui.CompactReadClassification{Kind: "skill", Label: label}, true
	}
	if label, ok := pigDocsLabel(absolutePath, filepath.Join(ConfigRoot(), "docs")); ok {
		return tui.CompactReadClassification{Kind: "docs", Label: label}, true
	}
	if compactResourceFileNames[fileName] {
		return tui.CompactReadClassification{Kind: "resource", Label: FormatPathRelativeToCwdOrAbsolute(absolutePath, cwd)}, true
	}
	return tui.CompactReadClassification{}, false
}

// pigDocsLabel is upstream read.ts getPiDocsClassification for PiG's
// documentation bundle. Upstream labels README.md and docs/ and examples/
// relative to its package root. The system prompt points PiG's model at
// <docs>/README.md and resolves docs/... under <docs> (prompts docsSection),
// so a file there is labelled as upstream labels the same page: README.md,
// or docs/<path>.
func pigDocsLabel(absolutePath, docsRoot string) (string, bool) {
	relativePath, err := filepath.Rel(filepath.Clean(docsRoot), filepath.Clean(absolutePath))
	if err != nil || relativePath == "." || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) || filepath.IsAbs(relativePath) {
		return "", false
	}
	label := filepath.ToSlash(relativePath)
	if label == "README.md" {
		return label, true
	}
	return "docs/" + label, true
}
