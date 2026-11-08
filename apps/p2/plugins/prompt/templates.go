// Prompt templates and skill invocation, ported from apps/p2/plugins/prompt/templates.ts, which copies earendil-works/pi
// (MIT, see third-party/pi.LICENSE): coding-agent core/prompt-templates.ts (parseCommandArgs, substituteArgs, the template
// file format and expandPromptTemplate), utils/frontmatter.ts, and core/agent-session.ts (`_expandSkillCommand`,
// `parseSkillBlock`). No filesystem here: files are read by the caller through the session's environment.
package prompt

import (
	"context"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/codingagent/frontmatter"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Template is a prompt template.
type Template struct {
	Name         string
	Description  string
	ArgumentHint string
	Content      string
	// Source is where it came from: p2's data directory ("global") or the project's `.pi/prompts` ("project").
	Source string
	Path   string
}

// SkillFile is a skill.
type SkillFile struct {
	Name        string
	Description string
	// Path is the `SKILL.md` path in the session's environment.
	Path string
	// BaseDir is the skill's directory, the base for relative references.
	BaseDir string
}

// parseFrontmatter is pi's `parseFrontmatter`: YAML between leading `---` lines, the rest is the body (trimmed). Malformed
// YAML is an error, as it throws in pi.
func parseFrontmatter(content string) (map[string]any, string, error) {
	doc := frontmatter.Parse(content)
	return doc.Frontmatter, doc.Body, doc.Err
}

// ParseTemplate is pi's template file: the name from the file, `description` (or the first line, 60 characters) and
// `argument-hint`.
func ParseTemplate(fileName, content, source, path string) (Template, error) {
	fields, body, err := parseFrontmatter(content)
	if err != nil {
		return Template{}, err
	}
	name := strings.TrimSuffix(fileName, ".md")
	description, _ := fields["description"].(string)
	if description == "" {
		for _, line := range strings.Split(body, "\n") {
			if jsstring.Trim(line) == "" {
				continue
			}
			description = line
			if jsstring.Length(line) > 60 {
				description = jsstring.Slice(line, 0, 60) + "..."
			}
			break
		}
	}
	hint, _ := fields["argument-hint"].(string)
	return Template{Name: name, Description: description, ArgumentHint: hint, Content: body, Source: source, Path: path}, nil
}

// ParseCommandArgs is bash-style argument splitting with quotes (pi's `parseCommandArgs`).
func ParseCommandArgs(argsString string) []string {
	args := []string{}
	var current strings.Builder
	var quote rune
	for _, char := range argsString {
		switch {
		case quote != 0:
			if char == quote {
				quote = 0
			} else {
				current.WriteRune(char)
			}
		case char == '"' || char == '\'':
			quote = char
		case jsstring.IsSpace(char):
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(char)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}

var substitution = regexp.MustCompile(`\$\{(\d+|ARGUMENTS|@):-([^}]*)\}|\$\{@:(\d+)(?::(\d+))?\}|\$(ARGUMENTS|@|\d+)`)

// parseInt is `Number.parseInt(digits, 10)` of a run of digits, saturated (an index past any argument list is the same
// as a huge one).
func parseInt(digits string) int {
	number, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || number > math.MaxInt32 {
		return math.MaxInt32
	}
	return int(number)
}

// argument is `args[number - 1]`, "" when there is none.
func argument(args []string, number int) string {
	if number < 1 || number > len(args) {
		return ""
	}
	return args[number-1]
}

// SubstituteArgs is pi's `substituteArgs`: `$1`…, `$@`/`$ARGUMENTS`, `${N:-default}`, `${@:-default}`/
// `${ARGUMENTS:-default}`, `${@:N}` and `${@:N:L}`. Values are not substituted again.
func SubstituteArgs(content string, args []string) string {
	allArgs := strings.Join(args, " ")
	var out strings.Builder
	last := 0
	for _, match := range substitution.FindAllStringSubmatchIndex(content, -1) {
		out.WriteString(content[last:match[0]])
		last = match[1]
		group := func(at int) string {
			if match[2*at] < 0 {
				return ""
			}
			return content[match[2*at]:match[2*at+1]]
		}
		switch {
		case group(1) != "":
			target := group(1)
			value := allArgs
			if target != "@" && target != "ARGUMENTS" {
				value = argument(args, parseInt(target))
			}
			if value != "" {
				out.WriteString(value)
			} else {
				out.WriteString(group(2))
			}
		case group(3) != "":
			start := max(0, parseInt(group(3))-1)
			rest := args[min(start, len(args)):]
			if length := group(4); length != "" {
				rest = rest[:min(parseInt(length), len(rest))]
			}
			out.WriteString(strings.Join(rest, " "))
		case group(5) == "ARGUMENTS" || group(5) == "@":
			out.WriteString(allArgs)
		default:
			out.WriteString(argument(args, parseInt(group(5))))
		}
	}
	out.WriteString(content[last:])
	return out.String()
}

// SkillBlock is pi's skill block: the skill's body with its name and location, then the user's arguments. A file whose
// frontmatter does not parse is an error.
func SkillBlock(skill SkillFile, fileContent, args string) (string, error) {
	_, body, err := parseFrontmatter(fileContent)
	if err != nil {
		return "", err
	}
	block := "<skill name=\"" + skill.Name + "\" location=\"" + skill.Path + "\">\nReferences are relative to " + skill.BaseDir + ".\n\n" + jsstring.Trim(body) + "\n</skill>"
	if args != "" {
		return block + "\n\n" + args, nil
	}
	return block, nil
}

// Expanded is a typed command's expansion; Skill is the invoked skill's name.
type Expanded struct {
	Text  string
	Skill *string
}

// ReadSkill reads a skill's file: ok is false when it is missing; an error is the same as missing.
type ReadSkill func(ctx context.Context, skill SkillFile) (content string, ok bool, err error)

// ExpandPrompt is pi's order: `/skill:name args` becomes the skill block, then `/name args` the first template of that
// name (global before project). Anything else, unknown names and unreadable skills stay as typed.
func ExpandPrompt(ctx context.Context, text string, templates []Template, skills []SkillFile, readSkill ReadSkill) (Expanded, error) {
	if strings.HasPrefix(text, "/skill:") {
		name, args := text[len("/skill:"):], ""
		if space := strings.Index(text, " "); space != -1 {
			name, args = text[len("/skill:"):space], jsstring.Trim(text[space+1:])
		}
		var skill *SkillFile
		for at := range skills {
			if skills[at].Name == name {
				skill = &skills[at]
				break
			}
		}
		if skill == nil {
			return Expanded{Text: text}, nil
		}
		content, ok, err := readSkill(ctx, *skill)
		if err != nil || !ok {
			return Expanded{Text: text}, nil
		}
		block, err := SkillBlock(*skill, content, args)
		if err != nil {
			return Expanded{}, err
		}
		return Expanded{Text: block, Skill: &name}, nil
	}
	if !strings.HasPrefix(text, "/") {
		return Expanded{Text: text}, nil
	}
	// /^\/([^\s]+)(?:\s+([\s\S]*))?$/
	rest := text[1:]
	end := strings.IndexFunc(rest, jsstring.IsSpace)
	if end == -1 {
		end = len(rest)
	}
	name := rest[:end]
	if name == "" {
		return Expanded{Text: text}, nil
	}
	for _, template := range templates {
		if template.Name == name {
			return Expanded{Text: SubstituteArgs(template.Content, ParseCommandArgs(strings.TrimLeftFunc(rest[end:], jsstring.IsSpace)))}, nil
		}
	}
	return Expanded{Text: text}, nil
}
