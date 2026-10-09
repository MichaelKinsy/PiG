package wiring

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// sourceFile is one production source file of a non-Go SDK.
type sourceFile struct {
	path string // repository-relative
	text string
}

// textSDK is an SDK whose wire code is read as text: the Node runtime, the
// Python SDK and the Rust SDK. Each language names its wire method parameter
// `method`, so a function with that parameter whose body builds the call frame,
// or forwards the parameter to such a function, is a sender.
type textSDK struct {
	name  string
	files []sourceFile
	// def matches a function header; group 1 is the name, group 2 the parameters.
	def *regexp.Regexp
	// sink matches, all together, the call frame construction that carries the method parameter.
	sink []*regexp.Regexp
	// notifySink matches, all together, the notify frame construction that carries the method parameter.
	notifySink []*regexp.Regexp
	// python bodies end by indentation, the others at the closing brace.
	python bool
}

func readSourceTree(t *testing.T, rel, suffix string) []sourceFile {
	t.Helper()
	root := filepath.Join(moduleRoot, filepath.FromSlash(rel))
	var files []sourceFile
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			switch name {
			case "testdata", "shims", "harness", "__pycache__", "node_modules", "tests":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, suffix) || strings.HasPrefix(name, "test_") || strings.HasSuffix(name, "_tests.rs") || strings.HasSuffix(name, "_test.rs") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relPath, _ := filepath.Rel(moduleRoot, path)
		files = append(files, sourceFile{path: filepath.ToSlash(relPath), text: stripRustTests(name, string(data))})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no %s sources under %s", suffix, rel)
	}
	return files
}

var rustTestModule = regexp.MustCompile(`\n#\[cfg\(test\)\]\s*\n\s*mod \w+\s*\{`)

// stripRustTests drops a Rust file's inline #[cfg(test)] module, which is not production wiring.
func stripRustTests(name, text string) string {
	if !strings.HasSuffix(name, ".rs") {
		return text
	}
	if loc := rustTestModule.FindStringIndex(text); loc != nil {
		return text[:loc[0]]
	}
	return text
}

func sdkSources(t *testing.T) []*textSDK {
	t.Helper()
	return []*textSDK{
		{
			name:       "node",
			files:      readSourceTree(t, "coding/extension/host/subprocess/runtime-node", ".mjs"),
			def:        regexp.MustCompile(`(?m)^[ \t]*(?:async[ \t]+)?(?:function[ \t]+)?([A-Za-z_]\w*)[ \t]*\(([^()]*)\)[ \t]*\{`),
			sink:       []*regexp.Regexp{regexp.MustCompile(`call:\s*\{\s*method\b`)},
			notifySink: []*regexp.Regexp{regexp.MustCompile(`type:\s*"notify",\s*notify:\s*\{\s*method\b`)},
		},
		{
			name:       "py",
			files:      readSourceTree(t, "extensions/sdk-py/pig_sdk", ".py"),
			def:        regexp.MustCompile(`(?m)^[ \t]*def[ \t]+(\w+)\(([^)]*)\)`),
			sink:       []*regexp.Regexp{regexp.MustCompile(`"type":\s*"call"`), regexp.MustCompile(`\{"method":\s*method\b`)},
			notifySink: []*regexp.Regexp{regexp.MustCompile(`"type":\s*"notify",\s*"notify":\s*\{"method":\s*method\b`)},
			python:     true,
		},
		{
			name:       "rust",
			files:      readSourceTree(t, "extensions/sdk-rs/src", ".rs"),
			def:        regexp.MustCompile(`(?m)^[ \t]*(?:pub(?:\([^)]*\))?[ \t]+)?fn[ \t]+(\w+)(?:<[^>]*>)?\(([^)]*)\)`),
			sink:       []*regexp.Regexp{regexp.MustCompile(`Call\s*\{\s*method\b`)},
			notifySink: []*regexp.Regexp{regexp.MustCompile(`msg_type:\s*"notify"`), regexp.MustCompile(`\bmethod(?::\s*method\b|,)`)},
		},
	}
}

// function is one function definition in a text SDK.
type function struct {
	file   *sourceFile
	name   string
	params []string
	body   string
	line   int
}

func (s *textSDK) functions() []function {
	var out []function
	for i := range s.files {
		file := &s.files[i]
		for _, match := range s.def.FindAllStringSubmatchIndex(file.text, -1) {
			name := file.text[match[2]:match[3]]
			switch name {
			case "if", "for", "while", "switch", "catch", "return", "function":
				continue
			}
			var params []string
			for param := range strings.SplitSeq(file.text[match[4]:match[5]], ",") {
				param = strings.TrimSpace(param)
				param, _, _ = strings.Cut(param, ":")
				param, _, _ = strings.Cut(param, "=")
				params = append(params, strings.TrimSpace(param))
			}
			if len(params) > 0 && (params[0] == "self" || params[0] == "&self" || params[0] == "&mut self") {
				params = params[1:]
			}
			out = append(out, function{file: file, name: name, params: params, body: s.bodyAt(file.text, match[0]), line: strings.Count(file.text[:match[0]], "\n") + 1})
		}
	}
	return out
}

// bodyAt returns a function's text from its header to its end.
func (s *textSDK) bodyAt(text string, start int) string {
	lineStart := strings.LastIndexByte(text[:start], '\n') + 1
	indent := len(text[lineStart:]) - len(strings.TrimLeft(text[lineStart:], " \t"))
	rest := text[lineStart:]
	lines := strings.SplitAfter(rest, "\n")
	var body strings.Builder
	for i, line := range lines {
		body.WriteString(line)
		if i == 0 {
			if !s.python && strings.HasSuffix(strings.TrimSpace(line), "}") && strings.Count(line, "{") <= strings.Count(line, "}") {
				break
			}
			continue
		}
		trimmed := strings.TrimSpace(line)
		lead := len(line) - len(strings.TrimLeft(line, " \t"))
		if s.python {
			if trimmed != "" && lead <= indent {
				return strings.TrimSuffix(body.String(), line)
			}
			continue
		}
		if lead == indent && strings.HasPrefix(trimmed, "}") {
			break
		}
	}
	return body.String()
}

var stringArg = regexp.MustCompile(`^\s*"([^"\\]*)"\s*$`)

// callArgs returns the top-level arguments of every call to name in text, with each call's offset.
func callArgs(text, name string) (calls [][]string, offsets []int) {
	pattern := regexp.MustCompile(`(?:^|[^\w$])` + regexp.QuoteMeta(name) + `(?:::<[^>]*>)?\(`)
	for _, match := range pattern.FindAllStringIndex(text, -1) {
		open := match[1] - 1
		var args []string
		depth, last := 0, open+1
		var quote byte
	scan:
		for i := open; i < len(text); i++ {
			c := text[i]
			switch {
			case quote != 0:
				switch c {
				case '\\':
					i++
				case quote:
					quote = 0
				}
			case c == '"' || c == '\'' || c == '`':
				quote = c
			case c == '(' || c == '[' || c == '{':
				depth++
			case c == ')' || c == ']' || c == '}':
				depth--
				if depth == 0 {
					args = append(args, text[last:i])
					break scan
				}
			case c == ',' && depth == 1:
				args = append(args, text[last:i])
				last = i + 1
			}
		}
		calls = append(calls, args)
		offsets = append(offsets, match[0])
	}
	return calls, offsets
}

// sentMethods derives every method name the SDK sends in a call frame.
func (s *textSDK) sentMethods() methodSet { return s.sentFrames(s.sink) }

// sentNotifies derives every method name the SDK sends in a notify frame.
func (s *textSDK) sentNotifies() methodSet { return s.sentFrames(s.notifySink) }

// sentFrames derives every method name the SDK puts in the frame that sink matches.
func (s *textSDK) sentFrames(sink []*regexp.Regexp) methodSet {
	fns := s.functions()
	senders := map[string]int{} // function name -> index of its method parameter
	for _, fn := range fns {
		if index := slices.Index(fn.params, "method"); index >= 0 && matchesAll(sink, fn.body) {
			senders[fn.name] = index
		}
	}
	for changed := true; changed; {
		changed = false
		for _, fn := range fns {
			index := slices.Index(fn.params, "method")
			if index < 0 {
				continue
			}
			if _, done := senders[fn.name]; done {
				continue
			}
			for name, at := range senders {
				calls, _ := callArgs(fn.body, name)
				for _, args := range calls {
					if at < len(args) && strings.TrimSpace(args[at]) == "method" {
						senders[fn.name] = index
						changed = true
					}
				}
			}
		}
	}
	sent := methodSet{}
	for i := range s.files {
		file := &s.files[i]
		for name, at := range senders {
			calls, offsets := callArgs(file.text, name)
			for j, args := range calls {
				if at < len(args) {
					if m := stringArg.FindStringSubmatch(args[at]); m != nil {
						sent.add(m[1], file.path+":"+strconv.Itoa(strings.Count(file.text[:offsets[j]], "\n")+1))
					}
				}
			}
		}
	}
	return sent
}

// contains reports whether a quoted literal appears in the SDK's production source.
func (s *textSDK) contains(literal string) bool {
	quoted := strconv.Quote(literal)
	for _, file := range s.files {
		if strings.Contains(file.text, quoted) {
			return true
		}
	}
	return false
}

func matchesAll(patterns []*regexp.Regexp, text string) bool {
	for _, pattern := range patterns {
		if !pattern.MatchString(text) {
			return false
		}
	}
	return true
}

var quoted = regexp.MustCompile(`"([^"\\]+)"`)

func quotedIn(text string) []string {
	var out []string
	for _, m := range quoted.FindAllStringSubmatch(text, -1) {
		out = append(out, m[1])
	}
	return out
}

// declaredObjectMethods derives the provider object methods the SDK puts in a
// native provider declaration's "methods" list, which tells the host which
// members the object implements.
func (s *textSDK) declaredObjectMethods(t *testing.T) methodSet {
	t.Helper()
	declared := methodSet{}
	add := func(file *sourceFile, offset int, names ...string) {
		at := file.path + ":" + strconv.Itoa(strings.Count(file.text[:offset], "\n")+1)
		for _, name := range names {
			declared.add(name, at)
		}
	}
	for i := range s.files {
		file := &s.files[i]
		switch s.name {
		case "node":
			// methods: paths.filter(...) keeps the members the object has.
			if m := regexp.MustCompile(`const methods = (\w+)\.filter`).FindStringSubmatch(file.text); m != nil {
				if list := regexp.MustCompile(`const ` + m[1] + ` = \[([^\]]*)\]`).FindStringSubmatchIndex(file.text); list != nil {
					add(file, list[0], quotedIn(file.text[list[2]:list[3]])...)
				}
			}
		case "py":
			// "methods": [m for m in METHODS if callable(...)] keeps the members the object has.
			if m := regexp.MustCompile(`"methods": \[\w+ for \w+ in (\w+)`).FindStringSubmatch(file.text); m != nil {
				if list := regexp.MustCompile(`(?m)^` + m[1] + ` = \(([^)]*)\)`).FindStringSubmatchIndex(file.text); list != nil {
					add(file, list[0], quotedIn(file.text[list[2]:list[3]])...)
				}
			}
		case "rust":
			// The function that serializes "methods":methods builds the list it pushes to.
			for _, fn := range s.functions() {
				if fn.file != file || !strings.Contains(fn.body, `"methods":methods`) {
					continue
				}
				offset := strings.Index(file.text, fn.body)
				for _, pattern := range []string{`let mut methods = vec!\[([^\]]*)\]`, `methods\.push\(("[^"]+")\)`, `methods\.extend\(\[([^\]]*)\]\)`} {
					for _, m := range regexp.MustCompile(pattern).FindAllStringSubmatch(fn.body, -1) {
						add(file, offset, quotedIn(m[1])...)
					}
				}
				if strings.Contains(fn.body, "methods.push(method)") {
					for _, m := range regexp.MustCompile(`\(\s*"([^"]+)",\s*provider\.`).FindAllStringSubmatch(fn.body, -1) {
						add(file, offset, m[1])
					}
				}
			}
		}
	}
	if len(declared) == 0 {
		t.Fatalf("%s SDK: found no provider declaration methods list; update this check's extraction", s.name)
	}
	return declared
}
