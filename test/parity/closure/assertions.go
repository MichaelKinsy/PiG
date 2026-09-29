package closure

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type AssertionCandidate struct {
	ID        string
	TestID    string
	Path      string
	Line      int
	Kind      string
	Callee    string
	TextHash  string
	Enforcing bool
}

type AssertionAuditRow struct {
	TestID     string
	Path       string
	Bound      int
	Candidates int
	Status     string
}

func ExtractAssertionCandidates(root string, test *Test, pin *Pin) ([]AssertionCandidate, error) {
	if pin.Repository != "pig" {
		return nil, nil
	}
	path := filepath.Join(root, filepath.FromSlash(pin.Path))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read assertion source %s: %w", pin.Path, err)
	}
	switch filepath.Ext(pin.Path) {
	case ".go":
		return extractGoAssertionCandidates(test.ID, pin, data)
	case ".toml":
		return extractScenarioAssertionCandidates(test.ID, pin, data)
	default:
		return nil, nil
	}
}

func extractGoAssertionCandidates(testID string, pin *Pin, data []byte) ([]AssertionCandidate, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, pin.Path, data, 0)
	if err != nil {
		return nil, fmt.Errorf("parse assertion source %s: %w", pin.Path, err)
	}
	testingReceivers := testingReceiverNames(file)
	var candidates []AssertionCandidate
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		position := fileSet.Position(call.Pos())
		if position.Line < pin.StartLine || position.Line > pin.EndLine {
			return true
		}
		kind, callee, enforcing, ok := classifyGoAssertionCall(call, testingReceivers)
		if !ok {
			return true
		}
		end := fileSet.Position(call.End()).Offset
		start := position.Offset
		if start < 0 || end < start || end > len(data) {
			return true
		}
		candidates = append(candidates, AssertionCandidate{
			ID:     "assertion-candidate:" + idDigest(fmt.Sprintf("%s\x00%s\x00%d\x00%s", testID, pin.Path, position.Line, callee)),
			TestID: testID, Path: pin.Path, Line: position.Line, Kind: kind, Callee: callee,
			TextHash: HashBytes(bytes.TrimSpace(data[start:end])), Enforcing: enforcing,
		})
		return true
	})
	slices.SortFunc(candidates, compareAssertionCandidates)
	return candidates, nil
}

func testingReceiverNames(file *ast.File) map[string]struct{} {
	names := make(map[string]struct{})
	ast.Inspect(file, func(node ast.Node) bool {
		function, ok := node.(*ast.FuncType)
		if !ok || function.Params == nil {
			return true
		}
		for _, field := range function.Params.List {
			if !isTestingType(field.Type) {
				continue
			}
			for _, name := range field.Names {
				names[name.Name] = struct{}{}
			}
		}
		return true
	})
	return names
}

func isTestingType(expression ast.Expr) bool {
	if pointer, ok := expression.(*ast.StarExpr); ok {
		expression = pointer.X
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	owner, ok := selector.X.(*ast.Ident)
	return ok && owner.Name == "testing" && (selector.Sel.Name == "T" || selector.Sel.Name == "B" || selector.Sel.Name == "TB")
}

func classifyGoAssertionCall(call *ast.CallExpr, testingReceivers map[string]struct{}) (string, string, bool, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", "", false, false
	}
	owner, ok := selector.X.(*ast.Ident)
	if !ok {
		return "", "", false, false
	}
	callee := owner.Name + "." + selector.Sel.Name
	if _, testingReceiver := testingReceivers[owner.Name]; testingReceiver {
		switch selector.Sel.Name {
		case "Error", "Errorf", "Fatal", "Fatalf", "Fail", "FailNow":
			return "testing-failure", callee, true, true
		}
	}
	if owner.Name == "assert" || owner.Name == "require" {
		return "testify", callee, true, true
	}
	if owner.Name == "cmp" && selector.Sel.Name == "Diff" {
		return "go-cmp-comparison", callee, false, true
	}
	return "", "", false, false
}

func extractScenarioAssertionCandidates(testID string, pin *Pin, data []byte) ([]AssertionCandidate, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	line := 0
	inAssert := false
	var candidates []AssertionCandidate
	for scanner.Scan() {
		line++
		if line < pin.StartLine || line > pin.EndLine {
			continue
		}
		text := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]") {
			inAssert = text == "[assert]"
			continue
		}
		if !inAssert || text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, _, ok := strings.Cut(text, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if !scenarioAssertionKey(key) {
			continue
		}
		candidates = append(candidates, AssertionCandidate{
			ID:     "assertion-candidate:" + idDigest(fmt.Sprintf("%s\x00%s\x00%d\x00%s", testID, pin.Path, line, key)),
			TestID: testID, Path: pin.Path, Line: line, Kind: "parity-comparator", Callee: key,
			TextHash: HashBytes([]byte(text)), Enforcing: true,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan assertion source %s: %w", pin.Path, err)
	}
	slices.SortFunc(candidates, compareAssertionCandidates)
	return candidates, nil
}

func scenarioAssertionKey(key string) bool {
	switch key {
	case "runs", "runtime_ratio_max":
		return false
	default:
		return key != ""
	}
}

func AssertionAudit(ctx context.Context, databasePath, root string) ([]byte, error) {
	database, err := openStore(databasePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = database.Close() }()
	records, err := readRecords(ctx, database)
	if err != nil {
		return nil, err
	}
	graph, err := Build(records)
	if err != nil {
		return nil, err
	}
	boundByTest := make(map[string]int)
	for _, assertionID := range graph.recordIDs(KindAssertion) {
		assertion := graph.Records[assertionID].(*Assertion)
		boundByTest[assertion.TestID]++
	}
	rows := make([]AssertionAuditRow, 0, len(boundByTest))
	for _, testID := range graph.recordIDs(KindTest) {
		bound := boundByTest[testID]
		if bound == 0 {
			continue
		}
		test := graph.Records[testID].(*Test)
		pin := graph.Records[test.PinID].(*Pin)
		candidates, err := ExtractAssertionCandidates(root, test, pin)
		if err != nil {
			return nil, err
		}
		enforcing := 0
		for _, candidate := range candidates {
			if candidate.Enforcing {
				enforcing++
			}
		}
		status := "candidate-present"
		if enforcing == 0 {
			status = "assertionless"
		}
		rows = append(rows, AssertionAuditRow{TestID: testID, Path: pin.Path, Bound: bound, Candidates: enforcing, Status: status})
	}
	slices.SortFunc(rows, func(left, right AssertionAuditRow) int { return cmp.Compare(left.TestID, right.TestID) })
	var output bytes.Buffer
	output.WriteString("test\tpath\tbound\tcandidates\tstatus\n")
	for _, row := range rows {
		fmt.Fprintf(&output, "%s\t%s\t%d\t%d\t%s\n", row.TestID, row.Path, row.Bound, row.Candidates, row.Status)
	}
	return output.Bytes(), nil
}

func compareAssertionCandidates(left, right AssertionCandidate) int {
	return cmp.Or(cmp.Compare(left.Path, right.Path), cmp.Compare(left.Line, right.Line), cmp.Compare(left.Callee, right.Callee))
}
