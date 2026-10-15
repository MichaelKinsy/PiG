package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The extractor describes a type alias whose body is an object literal by the alias head alone (`ToolExecutionResult<TDetails>`), so its
// shape hash does not change when members are renamed, added or retyped. aliasBodyHashes reads the body of every top-level type-alias
// entry from the upstream source, so a version leap can tell a carried alias row whose body changed from one that did not.

var (
	aliasHeadRe    = regexp.MustCompile(`^\s*(?:export\s+)?(?:declare\s+)?type\s+\w+`)
	blockCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)
	spaceRe        = regexp.MustCompile(`\s+`)
	typeAliasKind  = "type-alias"
)

// aliasBodyHashes maps the ID of each top-level type-alias entry to the hash of its comment-free, whitespace-normalised body in root.
// An entry whose source is not in root (an alias the inventory located in a published declaration file) or cannot be parsed is absent.
func aliasBodyHashes(root string, entries []inventoryEntry) map[string]string {
	hashes := map[string]string{}
	files := map[string][]string{}
	for _, entry := range entries {
		if entry.Kind != typeAliasKind || entry.ParentID != "" {
			continue
		}
		var source struct {
			Path string `json:"path"`
			Line int    `json:"line"`
		}
		if json.Unmarshal(entry.Source, &source) != nil || source.Path == "" || source.Line < 1 {
			continue
		}
		lines, ok := files[source.Path]
		if !ok {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(source.Path)))
			if err == nil {
				lines = strings.Split(string(data), "\n")
			}
			files[source.Path] = lines
		}
		if source.Line > len(lines) {
			continue
		}
		if body, ok := aliasDeclarationBody(strings.Join(lines[source.Line-1:], "\n")); ok {
			sum := sha256.Sum256([]byte(body))
			hashes[entry.ID] = hex.EncodeToString(sum[:])
		}
	}
	return hashes
}

// aliasDeclarationBody returns the normalised text between the `=` of the type alias that starts text and its closing semicolon.
func aliasDeclarationBody(text string) (string, bool) {
	head := aliasHeadRe.FindString(text)
	if head == "" {
		return "", false
	}
	text = text[len(head):]
	depth := 0
	eq := -1
scanHead:
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '<', '(', '[':
			depth++
		case '>':
			if depth > 0 && (i == 0 || text[i-1] != '=') {
				depth--
			}
		case ')', ']':
			depth--
		case '=':
			if depth == 0 {
				eq = i
				break scanHead
			}
		}
	}
	if eq < 0 {
		return "", false
	}
	body := text[eq+1:]
	depth = 0
	var quote byte
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case quote != 0:
			switch c {
			case '\\':
				i++
			case quote:
				quote = 0
			}
		case c == '/' && i+1 < len(body) && body[i+1] == '*':
			end := strings.Index(body[i+2:], "*/")
			if end < 0 {
				return "", false
			}
			i += end + 3
		case c == '/' && i+1 < len(body) && body[i+1] == '/':
			for i < len(body) && body[i] != '\n' {
				i++
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '{' || c == '(' || c == '[' || c == '<':
			depth++
		case c == '}' || c == ')' || c == ']':
			depth--
		case c == '>':
			if i == 0 || body[i-1] != '=' {
				depth--
			}
		case c == ';' && depth <= 0:
			return normalizeAliasBody(body[:i]), true
		}
	}
	return "", false
}

func normalizeAliasBody(body string) string {
	body = blockCommentRe.ReplaceAllString(body, "")
	var kept []string
	for line := range strings.SplitSeq(body, "\n") {
		if i := strings.Index(line, "//"); i >= 0 && !strings.ContainsAny(line[:i], `"'`+"`") {
			line = line[:i]
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(spaceRe.ReplaceAllString(strings.Join(kept, " "), " "))
}

// unchangedAliasBody returns the carry veto of pendingMappingLedger for a leap from the source at previousRoot (described by the
// inventory that backs the previous ledger at previousMapping's version) to the source at root. Only a type alias can be vetoed: an
// alias that both roots describe and whose body hash differs is changed; an alias that either root cannot describe is not judged, because
// the inventory locates some in published declaration files that the source mirror does not hold.
func unchangedAliasBody(upstream inventory, root, previousRoot, previousMapping string) func(string) bool {
	previousInventory := filepath.Join(filepath.Dir(previousMapping), strings.Replace(filepath.Base(previousMapping), "mapping-", "upstream-", 1))
	var previous inventory
	if err := decodeJSONFile(previousInventory, &previous); err != nil {
		exitf("interface-inventory: previous inventory for the alias body check: %v", err)
	}
	now := aliasBodyHashes(root, upstream.Interfaces)
	before := aliasBodyHashes(previousRoot, previous.Interfaces)
	isAlias := map[string]bool{}
	for _, entry := range upstream.Interfaces {
		if entry.Kind == typeAliasKind && entry.ParentID == "" {
			isAlias[entry.ID] = true
		}
	}
	return func(id string) bool {
		if !isAlias[id] {
			return true
		}
		a, okNow := now[id]
		b, okBefore := before[id]
		return !okNow || !okBefore || a == b
	}
}
