package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/text"
)

// HarnessTruncationResult includes retained text beside source-side truncation metadata.
type HarnessTruncationResult struct {
	Content string `json:"content"`
	harness.ShellOutputTruncation
}

func harnessTruncation(tr TruncationResult) harness.ShellOutputTruncation {
	var by *string
	if tr.TruncatedBy != "" {
		by = new(tr.TruncatedBy)
	}
	return harness.ShellOutputTruncation{Truncated: tr.Truncated, TruncatedBy: by, TotalLines: tr.TotalLines, TotalBytes: tr.TotalBytes, OutputLines: tr.OutputLines, OutputBytes: tr.OutputBytes, LastLinePartial: tr.LastLinePartial, FirstLineExceedsLimit: tr.FirstLineExceedsLimit, MaxLines: tr.MaxLines, MaxBytes: tr.MaxBytes}
}

// HarnessReadToolDetails describes truncated reads.
type HarnessReadToolDetails struct {
	Truncation *HarnessTruncationResult `json:"truncation,omitempty"`
}

// ReadImageProcessorResult is either a converted image or an omission message.
type ReadImageProcessorResult struct {
	OK       bool
	Data     string
	MimeType string
	Hints    []string
	Message  string
}

// ReadToolOptions controls an optional image processor.
type ReadToolOptions struct {
	AutoResizeImages *bool
	ImageProcessor   func(context.Context, []byte, string, bool) (ReadImageProcessorResult, error)
}

// Ports packages/agent/src/harness/tools/read.ts.
// CreateReadTool reads through the turn's execution environment. An image
// processor is optional; without one, supported image bytes pass through and
// BMP is omitted with the upstream configuration notice.
func CreateReadTool(options *ReadToolOptions) *harness.AgentHarnessTool {
	schema := (&ReadTool{}).Schema()
	return &harness.AgentHarnessTool{ToolSchema: ai.ToolSchema{Name: schema.Name, Description: schema.Description, Parameters: schema.Parameters}, Label: "read", Execute: func(ctx context.Context, _ string, params map[string]any, _ harness.AgentHarnessToolUpdateCallback, toolContext any, _ harness.AgentHarnessToolInvocation) (harness.AgentToolResult, error) {
		var p readParams
		if err := harnessParams(params, &p); err != nil {
			return harness.AgentToolResult{}, err
		}
		env, err := harnessToolEnv(toolContext)
		if err != nil {
			return harness.AgentToolResult{}, err
		}
		path, err := resolveHarnessReadPath(ctx, env, p.Path)
		if err != nil {
			return harness.AgentToolResult{}, err
		}
		data, err := env.ReadBinaryFile(ctx, path)
		if err != nil {
			return harness.AgentToolResult{}, err
		}
		if mime := SupportedImageMime(data); mime != "" {
			return harnessReadImage(ctx, data, mime, options)
		}
		var decoder utf8StreamDecoder
		result, err := readTextResult(p, strings.TrimPrefix(decoder.decode(data, false), "\ufeff"))
		if err != nil {
			return harness.AgentToolResult{}, err
		}
		out := harness.AgentToolResult{Content: result.Content}
		if details, ok := result.Details.(*ReadDetails); ok && details.Truncation != nil {
			tr := *details.Truncation
			out.Details = &HarnessReadToolDetails{Truncation: &HarnessTruncationResult{Content: tr.Content, ShellOutputTruncation: harnessTruncation(tr)}}
		}
		return out, nil
	}}
}
func harnessReadImage(ctx context.Context, data []byte, mime string, options *ReadToolOptions) (harness.AgentToolResult, error) {
	if options != nil && options.ImageProcessor != nil {
		processed, err := options.ImageProcessor(ctx, data, mime, options.AutoResizeImages == nil || *options.AutoResizeImages)
		if err != nil {
			return harness.AgentToolResult{}, err
		}
		if !processed.OK {
			return harnessText("Read image file [" + mime + "]\n" + processed.Message), nil
		}
		notice := "Read image file [" + processed.MimeType + "]"
		if len(processed.Hints) > 0 {
			notice += "\n" + strings.Join(processed.Hints, "\n")
		}
		out := harnessText(notice)
		out.Content = append(out.Content, ai.ImageContent{Data: processed.Data, MimeType: processed.MimeType})
		return out, nil
	}
	if mime == "image/bmp" {
		return harnessText("Read image file [image/bmp]\n[Image omitted: configure an imageProcessor to convert BMP images.]"), nil
	}
	out := harnessText("Read image file [" + mime + "]")
	out.Content = append(out.Content, ai.ImageContent{Data: base64.StdEncoding.EncodeToString(data), MimeType: mime})
	return out, nil
}

// Ports packages/agent/src/harness/tools/write.ts.
// CreateWriteTool serializes writes by environment and canonical path.
func CreateWriteTool() *harness.AgentHarnessTool {
	schema := (&WriteTool{}).Schema()
	return &harness.AgentHarnessTool{ToolSchema: ai.ToolSchema{Name: schema.Name, Description: schema.Description, Parameters: schema.Parameters}, Label: "write", Execute: func(ctx context.Context, _ string, params map[string]any, _ harness.AgentHarnessToolUpdateCallback, toolContext any, _ harness.AgentHarnessToolInvocation) (harness.AgentToolResult, error) {
		var p writeParams
		if err := harnessParams(params, &p); err != nil {
			return harness.AgentToolResult{}, err
		}
		env, err := harnessToolEnv(toolContext)
		if err != nil {
			return harness.AgentToolResult{}, err
		}
		path, err := resolveHarnessToolPath(ctx, env, p.Path)
		if err != nil {
			return harness.AgentToolResult{}, err
		}
		return withHarnessFileMutation(ctx, env, path, func() (harness.AgentToolResult, error) {
			if err := harnessAbort(ctx); err != nil {
				return harness.AgentToolResult{}, err
			}
			if err := env.WriteFile(ctx, path, []byte(p.Content)); err != nil {
				return harness.AgentToolResult{}, err
			}
			if err := harnessAbort(ctx); err != nil {
				return harness.AgentToolResult{}, err
			}
			return harnessText("Successfully wrote to " + p.Path), nil
		})
	}}
}

// HarnessEditToolDetails carries display and unified diffs.
type HarnessEditToolDetails struct {
	Diff             string `json:"diff"`
	Patch            string `json:"patch"`
	FirstChangedLine *int   `json:"firstChangedLine,omitempty"`
}

// Ports packages/agent/src/harness/tools/edit.ts.
// CreateEditTool applies disjoint replacements against one original file view.
func CreateEditTool() *harness.AgentHarnessTool {
	legacy := &EditTool{}
	schema := legacy.Schema()
	return &harness.AgentHarnessTool{ToolSchema: ai.ToolSchema{Name: schema.Name, Description: schema.Description, Parameters: schema.Parameters}, Label: "edit", PrepareArguments: func(args any) (any, error) {
		raw, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		raw, err = legacy.PrepareArguments(raw)
		if err != nil {
			return nil, err
		}
		var out any
		err = json.Unmarshal(raw, &out)
		return out, err
	}, Execute: func(ctx context.Context, _ string, params map[string]any, _ harness.AgentHarnessToolUpdateCallback, toolContext any, _ harness.AgentHarnessToolInvocation) (harness.AgentToolResult, error) {
		var p editParams
		if err := harnessParams(params, &p); err != nil {
			return harness.AgentToolResult{}, err
		}
		if len(p.Edits) == 0 {
			return harness.AgentToolResult{}, errors.New("Edit tool input is invalid. edits must contain at least one replacement.")
		}
		env, err := harnessToolEnv(toolContext)
		if err != nil {
			return harness.AgentToolResult{}, err
		}
		path, err := resolveHarnessToolPath(ctx, env, p.Path)
		if err != nil {
			return harness.AgentToolResult{}, err
		}
		return withHarnessFileMutation(ctx, env, path, func() (harness.AgentToolResult, error) { return executeHarnessEdit(ctx, env, path, p) })
	}}
}
func executeHarnessEdit(ctx context.Context, env harness.ExecutionEnv, path string, p editParams) (harness.AgentToolResult, error) {
	if err := harnessAbort(ctx); err != nil {
		return harness.AgentToolResult{}, err
	}
	info, err := env.FileInfo(ctx, path)
	if err != nil {
		return harness.AgentToolResult{}, harnessEditAccessError(p.Path, err)
	}
	if info.Kind != harness.FileKindFile && info.Kind != harness.FileKindSymlink {
		return harness.AgentToolResult{}, fmt.Errorf("Could not edit file: %s. Path is not a file.", p.Path)
	}
	original, err := env.ReadTextFile(ctx, path)
	if err != nil {
		return harness.AgentToolResult{}, harnessEditAccessError(p.Path, err)
	}
	if err := harnessAbort(ctx); err != nil {
		return harness.AgentToolResult{}, err
	}
	bom, content := text.SplitBom(original)
	ending := detectLineEnding(content)
	applied, err := applyEditsToNormalizedContent(normalizeToLF(content), p.Edits, p.Path)
	if err != nil {
		return harness.AgentToolResult{}, err
	}
	if err := harnessAbort(ctx); err != nil {
		return harness.AgentToolResult{}, err
	}
	if err := env.WriteFile(ctx, path, []byte(bom+restoreLineEndings(applied.newContent, ending))); err != nil {
		return harness.AgentToolResult{}, harnessEditAccessError(p.Path, err)
	}
	if err := harnessAbort(ctx); err != nil {
		return harness.AgentToolResult{}, err
	}
	diff, first := GenerateDiffString(applied.baseContent, applied.newContent)
	out := harnessText(fmt.Sprintf("Successfully replaced %d block(s) in %s.", len(p.Edits), p.Path))
	details := &HarnessEditToolDetails{Diff: diff, Patch: GenerateUnifiedPatch(p.Path, applied.baseContent, applied.newContent)}
	if first > 0 {
		details.FirstChangedLine = new(first)
	}
	out.Details = details
	return out, nil
}
