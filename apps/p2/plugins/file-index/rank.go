// Package fileindex is p2's file-index plugin (apps/p2/plugins/file-index): `@` and Tab file completion over a
// conversation's workspace.
package fileindex

import (
	"math"
	"slices"
	"strings"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/jsstring/localecompare"
	"github.com/MichaelKinsy/PiG/tui"
)

// Ports apps/p2/plugins/file-index/rank.ts: ranking for `@` completion, after pi-tui's autocomplete (`scoreEntry` over fd's
// substring matches, then depth, length and name). p2 has no fd query, so entries that match only fuzzily (pi-tui
// fuzzy.ts) rank below every substring match instead of being dropped. Pure; shared by the worker index and tests.

// FileItem is a file or a directory of the index: its path relative to the session directory.
type FileItem struct {
	Path string
	Dir  bool
}

func (item FileItem) json() *delta.JsonObject {
	return delta.JsonObjectOf("path", item.Path, "dir", item.Dir)
}

func basename(path string) string { return path[strings.LastIndex(path, "/")+1:] }

// ScoreEntry is pi's `scoreEntry`: exact name 100, name prefix 80, name substring 50, path substring 30; directories +10.
func ScoreEntry(path, query string, dir bool) float64 {
	name := jsstring.ToLower(basename(path))
	lower := jsstring.ToLower(query)
	score := 0.0
	switch {
	case name == lower:
		score = 100
	case strings.HasPrefix(name, lower):
		score = 80
	case strings.Contains(name, lower):
		score = 50
	case strings.Contains(jsstring.ToLower(path), lower):
		score = 30
	default:
		// p2: a fuzzy match (characters in order) of the name, else of the path, below every substring match.
		fuzzy := tui.FuzzyMatchScore(lower, name)
		if !fuzzy.Matches {
			fuzzy = tui.FuzzyMatchScore(lower, path)
		}
		if fuzzy.Matches {
			score = math.Max(1, math.Min(20, 20-fuzzy.Score/10))
		}
	}
	if dir && score > 0 {
		score += 10
	}
	return score
}

// Kind is which entries a completion asks for.
type Kind string

// The kinds of entries.
const (
	KindAny  Kind = "any"
	KindFile Kind = "file"
	KindDir  Kind = "dir"
)

// RankFiles is the best limit entries of entries for query, in completion order.
func RankFiles(entries []FileItem, query string, limit int, kind Kind) []FileItem {
	text := strings.TrimPrefix(query, "./")
	type scored struct {
		item  FileItem
		score float64
		depth int
	}
	var matches []scored
	for _, item := range entries {
		if kind == KindFile && item.Dir || kind == KindDir && !item.Dir {
			continue
		}
		score := 1.0
		if text != "" {
			score = ScoreEntry(item.Path, text, item.Dir)
		}
		if score > 0 {
			matches = append(matches, scored{item, score, strings.Count(item.Path, "/") + 1})
		}
	}
	slices.SortStableFunc(matches, func(a, b scored) int {
		if a.score != b.score {
			if b.score > a.score {
				return 1
			}
			return -1
		}
		if a.depth != b.depth {
			return a.depth - b.depth
		}
		if lengthA, lengthB := p2runtime.JSLength(a.item.Path), p2runtime.JSLength(b.item.Path); lengthA != lengthB {
			return lengthA - lengthB
		}
		return localecompare.Compare(a.item.Path, b.item.Path)
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	ranked := make([]FileItem, len(matches))
	for at, match := range matches {
		ranked[at] = match.item
	}
	return ranked
}

// WithDirectories is the files plus the directories their paths imply: the directories first, in the order their first
// file names them, then the files.
func WithDirectories(files []string) []FileItem {
	seen := map[string]bool{}
	var items []FileItem
	for _, file := range files {
		for at := strings.Index(file, "/"); at != -1; {
			if directory := file[:at]; !seen[directory] {
				seen[directory] = true
				items = append(items, FileItem{Path: directory, Dir: true})
			}
			next := strings.Index(file[at+1:], "/")
			if next == -1 {
				break
			}
			at += 1 + next
		}
	}
	for _, file := range files {
		items = append(items, FileItem{Path: file})
	}
	return items
}
