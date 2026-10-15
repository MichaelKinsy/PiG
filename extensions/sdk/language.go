package sdk

import "strings"

// languageByExtension is the table of Pi's getLanguageFromPath (modes/interactive/theme/theme.ts).
var languageByExtension = map[string]string{
	"ts":         "typescript",
	"tsx":        "typescript",
	"js":         "javascript",
	"jsx":        "javascript",
	"mjs":        "javascript",
	"cjs":        "javascript",
	"py":         "python",
	"rb":         "ruby",
	"rs":         "rust",
	"go":         "go",
	"java":       "java",
	"kt":         "kotlin",
	"swift":      "swift",
	"c":          "c",
	"h":          "c",
	"cpp":        "cpp",
	"cc":         "cpp",
	"cxx":        "cpp",
	"hpp":        "cpp",
	"cs":         "csharp",
	"php":        "php",
	"sh":         "bash",
	"bash":       "bash",
	"zsh":        "bash",
	"fish":       "fish",
	"ps1":        "powershell",
	"sql":        "sql",
	"html":       "html",
	"htm":        "html",
	"css":        "css",
	"scss":       "scss",
	"sass":       "sass",
	"less":       "less",
	"json":       "json",
	"yaml":       "yaml",
	"yml":        "yaml",
	"toml":       "toml",
	"xml":        "xml",
	"md":         "markdown",
	"markdown":   "markdown",
	"dockerfile": "dockerfile",
	"makefile":   "makefile",
	"cmake":      "cmake",
	"lua":        "lua",
	"perl":       "perl",
	"r":          "r",
	"scala":      "scala",
	"clj":        "clojure",
	"ex":         "elixir",
	"exs":        "elixir",
	"erl":        "erlang",
	"hs":         "haskell",
	"ml":         "ocaml",
	"vim":        "vim",
	"graphql":    "graphql",
	"proto":      "protobuf",
	"tf":         "hcl",
	"hcl":        "hcl",
}

// GetLanguageFromPath is Pi's getLanguageFromPath: the highlight language for the text after the last "." of a path, lower-cased as JavaScript's toLowerCase
// does (a path without a dot is its own extension, so "Dockerfile" is dockerfile), or ok=false where Pi returns undefined.
func GetLanguageFromPath(filePath string) (language string, ok bool) {
	extension := filePath
	if i := strings.LastIndex(filePath, "."); i >= 0 {
		extension = filePath[i+1:]
	}
	if extension == "" {
		return "", false
	}
	// toLowerCase applies Unicode's full mapping, where U+0130 is "i\u0307"; strings.ToLower's simple mapping makes it "i", which would turn "vİm" into vim.
	language, ok = languageByExtension[strings.ToLower(strings.ReplaceAll(extension, "\u0130", "i\u0307"))]
	return language, ok
}
