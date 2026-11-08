package theme

// lowlightLanguages are the highlight.js languages p2's lowlight `common` bundle registers (lowlight 3.3.0, highlight.js 11.11) with their aliases,
// each mapped to the language's name: p2 highlights nothing else (theme.ts Theme.language, `lowlight.registered`), so a language the full
// highlight.js knows but `common` leaves out, such as haskell or dockerfile, takes the `mdCodeBlock` color.
var lowlightLanguages = func() map[string]string {
	aliases := map[string][]string{
		"arduino": {"ino"}, "bash": {"sh", "zsh"}, "c": {"h"}, "cpp": {"cc", "c++", "h++", "hpp", "hh", "hxx", "cxx"}, "csharp": {"cs", "c#"},
		"css": nil, "diff": {"patch"}, "go": {"golang"}, "graphql": {"gql"}, "ini": {"toml"}, "java": {"jsp"},
		"javascript": {"js", "jsx", "mjs", "cjs"}, "json": {"jsonc", "json5"}, "kotlin": {"kt", "kts"}, "less": nil, "lua": {"pluto"},
		"makefile": {"mk", "mak", "make"}, "markdown": {"md", "mkdown", "mkd"}, "objectivec": {"mm", "objc", "obj-c", "obj-c++", "objective-c++"},
		"perl": {"pl", "pm"}, "php": nil, "php-template": nil, "plaintext": {"text", "txt"}, "python": {"py", "gyp", "ipython"},
		"python-repl": {"pycon"}, "r": nil, "ruby": {"rb", "gemspec", "podspec", "thor", "irb"}, "rust": {"rs"}, "scss": nil,
		"shell": {"console", "shellsession"}, "sql": nil, "swift": nil, "typescript": {"ts", "tsx", "mts", "cts"}, "vbnet": {"vb"},
		"wasm": nil, "xml": {"html", "xhtml", "rss", "atom", "xjb", "xsd", "xsl", "plist", "wsf", "svg"}, "yaml": {"yml"},
	}
	set := make(map[string]string, 128)
	for name, names := range aliases {
		set[name] = name
		for _, alias := range names {
			set[alias] = name
		}
	}
	return set
}()
