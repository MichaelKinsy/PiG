package closure

import (
	"bufio"
	"bytes"
	"cmp"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type CoverageBlock struct {
	Path        string
	StartLine   int
	StartColumn int
	EndLine     int
	EndColumn   int
	Statements  int
	Count       int
}

type BranchOutcome struct {
	ID        string
	Path      string
	Kind      string
	Outcome   string
	StartLine int
	EndLine   int
	Status    string
}

type CoverageAudit struct {
	Blocks   []CoverageBlock
	Branches []BranchOutcome
}

type CoverageScope struct {
	Path      string
	StartLine int
	EndLine   int
}

func AuditGoCoverage(root string, profile io.Reader, paths []string) (CoverageAudit, error) {
	scopes := make([]CoverageScope, 0, len(paths))
	for _, path := range paths {
		scopes = append(scopes, CoverageScope{Path: path, StartLine: 1, EndLine: int(^uint(0) >> 1)})
	}
	return AuditGoCoverageScopes(root, profile, scopes)
}

func AuditGoCoverageScopes(root string, profile io.Reader, scopes []CoverageScope) (CoverageAudit, error) {
	blocks, err := ParseGoCoverage(root, profile)
	if err != nil {
		return CoverageAudit{}, err
	}
	selected := make(map[string][]CoverageScope, len(scopes))
	for _, scope := range scopes {
		scope.Path = filepath.ToSlash(scope.Path)
		if scope.StartLine < 1 || scope.EndLine < scope.StartLine {
			return CoverageAudit{}, fmt.Errorf("coverage scope %s has invalid line range %d-%d", scope.Path, scope.StartLine, scope.EndLine)
		}
		selected[scope.Path] = append(selected[scope.Path], scope)
	}
	if len(selected) > 0 {
		blocks = slices.DeleteFunc(blocks, func(block CoverageBlock) bool {
			return !coverageBlockInScopes(block, selected[block.Path])
		})
	}
	files := make(map[string]struct{})
	for _, block := range blocks {
		files[block.Path] = struct{}{}
	}
	for path := range selected {
		files[path] = struct{}{}
	}
	filePaths := make([]string, 0, len(files))
	for path := range files {
		filePaths = append(filePaths, path)
	}
	slices.Sort(filePaths)
	var branches []BranchOutcome
	for _, path := range filePaths {
		extracted, err := extractGoBranches(root, path, blocks)
		if err != nil {
			return CoverageAudit{}, err
		}
		if fileScopes := selected[path]; len(fileScopes) > 0 {
			extracted = slices.DeleteFunc(extracted, func(branch BranchOutcome) bool {
				return !lineRangeInScopes(branch.StartLine, branch.EndLine, fileScopes)
			})
		}
		branches = append(branches, extracted...)
	}
	slices.SortFunc(blocks, compareCoverageBlocks)
	slices.SortFunc(branches, func(left, right BranchOutcome) int { return cmp.Compare(left.ID, right.ID) })
	return CoverageAudit{Blocks: blocks, Branches: branches}, nil
}

func coverageBlockInScopes(block CoverageBlock, scopes []CoverageScope) bool {
	return lineRangeInScopes(block.StartLine, block.EndLine, scopes)
}

func lineRangeInScopes(startLine, endLine int, scopes []CoverageScope) bool {
	for _, scope := range scopes {
		if scope.StartLine <= endLine && scope.EndLine >= startLine {
			return true
		}
	}
	return false
}

func ParseGoCoverage(root string, profile io.Reader) ([]CoverageBlock, error) {
	scanner := bufio.NewScanner(io.LimitReader(profile, 512<<20))
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	lineNumber := 0
	var blocks []CoverageBlock
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if lineNumber == 1 {
			if !strings.HasPrefix(line, "mode: ") {
				return nil, fmt.Errorf("coverage profile: first line is not a mode")
			}
			continue
		}
		if line == "" {
			continue
		}
		block, err := parseCoverageLine(root, line)
		if err != nil {
			return nil, fmt.Errorf("coverage profile line %d: %w", lineNumber, err)
		}
		blocks = append(blocks, block)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("coverage profile: %w", err)
	}
	if lineNumber == 0 {
		return nil, fmt.Errorf("coverage profile is empty")
	}
	slices.SortFunc(blocks, compareCoverageBlocks)
	return blocks, nil
}

func RenderCoverageAudit(audit CoverageAudit) []byte {
	var output bytes.Buffer
	output.WriteString("path\tkind\toutcome\tlines\tstatus\n")
	for _, branch := range audit.Branches {
		fmt.Fprintf(&output, "%s\t%s\t%s\t%d-%d\t%s\n", branch.Path, branch.Kind, branch.Outcome, branch.StartLine, branch.EndLine, branch.Status)
	}
	output.WriteString("\npath\tblock\tstatements\tcount\n")
	for _, block := range audit.Blocks {
		if block.Count > 0 {
			continue
		}
		fmt.Fprintf(&output, "%s\t%d.%d-%d.%d\t%d\t%d\n", block.Path, block.StartLine, block.StartColumn, block.EndLine, block.EndColumn, block.Statements, block.Count)
	}
	return output.Bytes()
}

func parseCoverageLine(root, line string) (CoverageBlock, error) {
	fields := strings.Fields(line)
	if len(fields) != 3 {
		return CoverageBlock{}, fmt.Errorf("expected range, statements, and count")
	}
	comma := strings.IndexByte(fields[0], ',')
	if comma < 0 {
		return CoverageBlock{}, fmt.Errorf("range has no comma")
	}
	colon := strings.LastIndexByte(fields[0][:comma], ':')
	if colon < 0 {
		return CoverageBlock{}, fmt.Errorf("range has no path separator")
	}
	path, err := normalizeCoveragePath(root, fields[0][:colon])
	if err != nil {
		return CoverageBlock{}, err
	}
	startLine, startColumn, err := parsePosition(fields[0][colon+1 : comma])
	if err != nil {
		return CoverageBlock{}, fmt.Errorf("start: %w", err)
	}
	endLine, endColumn, err := parsePosition(fields[0][comma+1:])
	if err != nil {
		return CoverageBlock{}, fmt.Errorf("end: %w", err)
	}
	statements, err := strconv.Atoi(fields[1])
	if err != nil || statements < 0 {
		return CoverageBlock{}, fmt.Errorf("invalid statement count %q", fields[1])
	}
	count, err := strconv.Atoi(fields[2])
	if err != nil || count < 0 {
		return CoverageBlock{}, fmt.Errorf("invalid execution count %q", fields[2])
	}
	if startLine < 1 || endLine < startLine {
		return CoverageBlock{}, fmt.Errorf("invalid line range")
	}
	return CoverageBlock{
		Path: path, StartLine: startLine, StartColumn: startColumn, EndLine: endLine, EndColumn: endColumn,
		Statements: statements, Count: count,
	}, nil
}

func parsePosition(value string) (int, int, error) {
	line, column, ok := strings.Cut(value, ".")
	if !ok {
		return 0, 0, fmt.Errorf("position %q has no column", value)
	}
	lineNumber, lineErr := strconv.Atoi(line)
	columnNumber, columnErr := strconv.Atoi(column)
	if lineErr != nil || columnErr != nil || lineNumber < 1 || columnNumber < 1 {
		return 0, 0, fmt.Errorf("invalid position %q", value)
	}
	return lineNumber, columnNumber, nil
}

func normalizeCoveragePath(root, path string) (string, error) {
	path = filepath.ToSlash(path)
	path = strings.TrimPrefix(path, "github.com/MichaelKinsy/PiG/")
	// Tracked closure evidence predates the repository move. Normalize its
	// recorded module prefix without rewriting the attested artifact bytes.
	path = strings.TrimPrefix(path, "github.com/MichaelKinsy/PiG/")
	if filepath.IsAbs(path) {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return "", err
		}
		path = filepath.ToSlash(relative)
	}
	if path == ".." || strings.HasPrefix(path, "../") {
		return "", fmt.Errorf("coverage path escapes repository root")
	}
	return path, nil
}

func extractGoBranches(root, path string, blocks []CoverageBlock) ([]BranchOutcome, error) {
	fullPath := filepath.Join(root, filepath.FromSlash(path))
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, fullPath, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("extract branches %s: %w", path, err)
	}
	var branches []BranchOutcome
	add := func(kind, outcome string, start, end token.Pos, unknown bool) {
		startLine := fileSet.Position(start).Line
		endLine := fileSet.Position(end).Line
		status := "uncovered"
		if unknown {
			status = "unknown"
		} else if rangeCovered(path, startLine, endLine, blocks) {
			status = "covered"
		}
		idMaterial := fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d", path, kind, outcome, startLine, endLine)
		branches = append(branches, BranchOutcome{
			ID: "branch:" + idDigest(idMaterial), Path: path, Kind: kind, Outcome: outcome,
			StartLine: startLine, EndLine: endLine, Status: status,
		})
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.IfStmt:
			add("if", "true", value.Body.Lbrace, value.Body.Rbrace, false)
			if value.Else == nil {
				add("if", "false", value.If, value.Body.Rbrace, true)
			} else {
				add("if", "false", value.Else.Pos(), value.Else.End(), false)
			}
		case *ast.CaseClause:
			outcome := "default"
			if len(value.List) > 0 {
				outcome = fmt.Sprintf("case-%d", len(value.List))
			}
			add("switch", outcome, value.Colon, value.End(), len(value.Body) == 0)
		case *ast.CommClause:
			outcome := "default"
			if value.Comm != nil {
				outcome = "case"
			}
			add("select", outcome, value.Colon, value.End(), len(value.Body) == 0)
		case *ast.ForStmt:
			add("loop", "body", value.Body.Lbrace, value.Body.Rbrace, false)
			add("loop", "zero", value.For, value.Body.Rbrace, true)
		case *ast.RangeStmt:
			add("range", "body", value.Body.Lbrace, value.Body.Rbrace, false)
			add("range", "zero", value.For, value.Body.Rbrace, true)
		}
		return true
	})
	slices.SortFunc(branches, func(left, right BranchOutcome) int { return cmp.Compare(left.ID, right.ID) })
	return branches, nil
}

func rangeCovered(path string, startLine, endLine int, blocks []CoverageBlock) bool {
	for _, block := range blocks {
		if block.Path == path && block.Count > 0 && block.StartLine <= endLine && block.EndLine >= startLine {
			return true
		}
	}
	return false
}

func compareCoverageBlocks(left, right CoverageBlock) int {
	if path := cmp.Compare(left.Path, right.Path); path != 0 {
		return path
	}
	if left.StartLine != right.StartLine {
		return cmp.Compare(left.StartLine, right.StartLine)
	}
	return cmp.Compare(left.StartColumn, right.StartColumn)
}
