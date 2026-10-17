// Ports packages/coding-agent/src/cli/file-processor.ts.
package codingagent

import (
	"encoding/base64"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/imageprocessing"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
	"github.com/MichaelKinsy/PiG/internal/text"
)

var processFileImage = imageprocessing.ProcessImage

// ProcessFileOptions controls CLI attachment resizing. A nil AutoResizeImages uses the upstream default, true.
type ProcessFileOptions struct {
	AutoResizeImages *bool
}

// ProcessedCLIArgs is the upstream-shaped result of processing CLI @file
// arguments: text placeholders plus image attachments.
type ProcessedCLIArgs struct {
	Text   string
	Images []ai.ImageContent
}

// ProcessCLIFileArguments expands CLI @file arguments into text and images. Text files decode as UTF-8 with U+FFFD for each ill-formed subpart, drop a leading BOM, and use <file name="/abs/path">\ncontent\n</file>\n, including the separator when content ends in a newline. Images carry conversion or dimension hints; processing failures become omission notes. AutoResizeImages defaults to true and can be disabled until the request model is selected.
func ProcessCLIFileArguments(fileArgs []string, cwd string, options ...ProcessFileOptions) (ProcessedCLIArgs, error) {
	autoResize := true
	if len(options) > 0 && options[0].AutoResizeImages != nil {
		autoResize = *options[0].AutoResizeImages
	}
	if len(fileArgs) == 0 {
		return ProcessedCLIArgs{}, nil
	}
	var out strings.Builder
	var images []ai.ImageContent
	for _, arg := range fileArgs {
		// upstream: file-processor.ts `resolve(resolveReadPath(fileArg, process.cwd()))`: `~` expansion, the `@` prefix and the macOS screenshot name variants.
		absPath, err := tools.ResolveReadPath(arg, cwd)
		if err != nil {
			return ProcessedCLIArgs{}, err
		}
		// upstream: file-processor.ts `access`, `stat` and `readFile` take the path through toNamespacedPath, so on Windows the `\\?\` form reaches the filesystem and `a.txt ` or `a.txt.` is not the file `a.txt`, as the Win32 name normalization would make it. A path that cannot be made absolute fails `access` like a missing file.
		fsPath, err := nodepath.ToNamespacedPath(absPath)
		if err != nil {
			return ProcessedCLIArgs{}, fmt.Errorf("File not found: %s", absPath)
		}
		info, err := os.Stat(fsPath)
		if err != nil {
			return ProcessedCLIArgs{}, fmt.Errorf("File not found: %s", absPath)
		}
		// libuv (fs.c fs__stat_assign_statbuf) reports st_size 0 for a Windows directory, so `stats.size === 0` skips it where a POSIX directory fails in detectSupportedImageMimeTypeFromFile.
		if info.Size() == 0 || info.IsDir() && runtime.GOOS == "windows" {
			continue
		}
		content, err := os.ReadFile(fsPath)
		if err != nil {
			return ProcessedCLIArgs{}, fmt.Errorf("could not read file %s: %w", absPath, err)
		}
		if mime := DetectSupportedImageMimeType(content); mime != "" {
			resized, resizedMime, note, err := processFileImage(content, mime, autoResize, nil)
			if err != nil {
				out.WriteString(`<file name="` + absPath + `">` + err.Error() + "</file>\n")
				continue
			}
			images = append(images, ai.ImageContent{
				MimeType: resizedMime,
				Data:     base64.StdEncoding.EncodeToString(resized),
			})
			out.WriteString(`<file name="`)
			out.WriteString(absPath)
			out.WriteString(`">`)
			out.WriteString(note)
			out.WriteString("</file>\n")
			continue
		}
		// upstream: file-processor.ts `stripBom(await readFile(absolutePath, "utf-8"))`: each maximal ill-formed UTF-8 subpart decodes to one U+FFFD.
		out.WriteString(`<file name="`)
		out.WriteString(absPath)
		out.WriteString(`">`)
		out.WriteByte('\n')
		out.WriteString(text.StripBom(jsstring.FromUTF8(content)))
		out.WriteByte('\n')
		out.WriteString("</file>\n")
	}
	return ProcessedCLIArgs{Text: out.String(), Images: images}, nil
}
