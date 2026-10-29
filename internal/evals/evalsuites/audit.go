package evalsuites

// Ports packages/evals/evals/documentation-audit.eval.ts.

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/evals"
	"github.com/MichaelKinsy/PiG/internal/evals/bridge"
	"github.com/MichaelKinsy/PiG/internal/jsstring/localecompare"
)

const auditToolName = "submit_documentation_audit"

func submitAudit() evals.CustomTool {
	return evals.CustomTool{
		Tool: bridge.Tool{
			Name: auditToolName, Label: "Submit documentation audit",
			Description:   "Submit the final verdict after completing the documentation investigation.",
			PromptSnippet: "Submit the final documentation audit as validated structured data",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"verdict":     ai.StringEnum([]string{"match", "mismatch", "inconclusive"}, nil),
					"explanation": map[string]any{"type": "string", "minLength": 1, "maxLength": 2000},
				},
				"required": []string{"verdict", "explanation"}, "additionalProperties": false,
			},
			ConstrainedSampling: &bridge.ConstrainedSampling{Type: "json_schema", Strict: "prefer"},
		},
		Execute: func(_ context.Context, _ string, params map[string]any) (evals.CustomToolResult, error) {
			return evals.CustomToolResult{Content: "Documentation audit submitted.", Details: params, Terminate: true}, nil
		},
	}
}

// documentationPages lists the Markdown pages under docsRoot, sorted by path.
func documentationPages(docsRoot string) ([]string, error) {
	var pages []string
	err := filepath.WalkDir(docsRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			relative, err := filepath.Rel(docsRoot, path)
			pages = append(pages, filepath.ToSlash(relative))
			return err
		}
		return nil
	})
	// Pi sorts the pages with localeCompare.
	collator := localecompare.NewCollator()
	slices.SortStableFunc(pages, collator.CompareString)
	return pages, err
}

// readsImplementation reports whether path is under root and outside docsRoot (documentation-audit.eval.ts: relative(sourceRoot, path) neither starts with ".." nor is empty).
func readsImplementation(root, docsRoot, path string) bool {
	inside := func(directory string) bool {
		relative, err := filepath.Rel(directory, path)
		return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
	}
	return inside(root) && !inside(docsRoot)
}

// DocumentationAudit audits each documentation page against the implementation; the verdict arrives through a
// custom tool that ends the run.
func DocumentationAudit() evals.Suite {
	suite := evals.Suite{
		Name: "Audit documentation against implementation",
		File: evals.AuditEvalFile,
		Harness: func(ctx context.Context, input evals.PiCodingAgentInput) (*evals.HarnessRun, error) {
			return evals.RunPiCodingAgent(ctx, input, evals.PiCodingAgentHarnessOptions{
				Name: "documentation-page-audit", Tools: []string{"read", "grep", "find", "ls", auditToolName}, CustomTools: []evals.CustomTool{submitAudit()},
			})
		},
	}
	root, err := repositoryRoot()
	if err != nil {
		suite.Cases = []evals.Case{{Name: "locates the repository", Run: func(context.Context, evals.RunFunc) error { return err }}}
		return suite
	}
	// Pi audits the coding-agent docs it ships to the agent (packages/coding-agent/docs); PiG ships the embedded docs.
	docsRoot := filepath.Join(root, "internal", "pigdocs", "content")
	pages, err := documentationPages(docsRoot)
	if err != nil && !os.IsNotExist(err) {
		suite.Cases = []evals.Case{{Name: "lists the documentation pages", Run: func(context.Context, evals.RunFunc) error { return err }}}
		return suite
	}
	for _, page := range pages {
		documentationPath := filepath.Join(docsRoot, filepath.FromSlash(page))
		suite.Cases = append(suite.Cases, evals.Case{Name: page + " matches the implementation", Run: func(ctx context.Context, run evals.RunFunc) error {
			result, err := run(ctx, evals.PromptInput(fmt.Sprintf(`Audit this PiG documentation page against the repository implementation.

Documentation page: %s
Repository root: %s

Read the complete page and the relevant implementation. Report a mismatch only for a clear, user-visible contradiction between an explicit documentation claim and actual runtime behavior. Follow the runtime path; names, comments, types, isolated helpers, and tests are not sufficient evidence by themselves.

Missing internal detail, ambiguous wording, hypothetical misuse, and undocumented edge cases are not mismatches. If the evidence is not decisive, report inconclusive. Otherwise report match.

For a mismatch, quote the claim, cite the implementation path and symbol, and state the concrete behavior a user would observe.

Call %s exactly once as your final action. Do not return prose.`, documentationPath, root, auditToolName)))
			if err != nil {
				return err
			}
			// The audit must have read the page and some implementation: Pi requires a read under packages/coding-agent/src,
			// where PiG's implementation is every file of the repository outside the documentation it audits.
			calls := evals.ToolCalls(result.Events)
			var readPaths []string
			for _, call := range calls {
				if path, ok := call.Arguments["path"].(string); ok && call.Name == "read" && call.Status == "ok" {
					absolute, err := filepath.Abs(path)
					if err != nil {
						return err
					}
					readPaths = append(readPaths, absolute)
				}
			}
			if !slices.Contains(readPaths, documentationPath) {
				return fmt.Errorf("expected the audit to read %s, it read %v", documentationPath, readPaths)
			}
			if !slices.ContainsFunc(readPaths, func(path string) bool { return readsImplementation(root, docsRoot, path) }) {
				return fmt.Errorf("expected the audit to read the implementation under %s, it read %v", root, readPaths)
			}
			var auditCalls []evals.ToolCall
			for _, call := range calls {
				if call.Name == auditToolName {
					auditCalls = append(auditCalls, call)
				}
			}
			calls = auditCalls
			if len(calls) != 1 {
				return fmt.Errorf("expected one %s call, got %d", auditToolName, len(calls))
			}
			if calls[0].Status != "ok" {
				return fmt.Errorf("expected the audit call to be ok, got %s", calls[0].Status)
			}
			if verdict := calls[0].Arguments["verdict"]; verdict != "match" {
				explanation, _ := calls[0].Arguments["explanation"].(string)
				return fmt.Errorf("%s: expected verdict %v to be \"match\"", explanation, verdict)
			}
			return nil
		}})
	}
	return suite
}
