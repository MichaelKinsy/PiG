package experimental

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// Ports packages/coding-agent/src/cli/experimental/command.ts

// CommandOption describes a flag or a value option. Parse returns a CLI diagnostic on invalid input.
type CommandOption struct {
	Name       string
	Flag       bool
	Repeatable bool
	Parse      func(string) (any, error)
}

func ValueOption(name string, parse func(string) (any, error), repeatable bool) CommandOption {
	return CommandOption{Name: name, Parse: parse, Repeatable: repeatable}
}

func StringOption(name string, repeatable bool) CommandOption {
	return ValueOption(name, func(value string) (any, error) { return value, nil }, repeatable)
}

func FlagOption(name string) CommandOption {
	return CommandOption{Name: name, Flag: true, Parse: func(string) (any, error) { return true, nil }}
}

// ParsedCommandInput retains the first unrecognized argument and every argument after it, including a -- separator.
type ParsedCommandInput struct {
	RemainingArgs []string
	values        map[string][]any
}

func (p ParsedCommandInput) Value(option CommandOption) any {
	if values := p.values[option.Name]; len(values) > 0 {
		return values[0]
	}
	return nil
}

func (p ParsedCommandInput) Values(option CommandOption) []any {
	return p.values[option.Name]
}

// Command composes options, a builder, an awaited action, and subcommands. Configuration completes before concurrent parsing or execution.
type Command struct {
	Name          string
	options       map[string]CommandOption
	subcommands   map[string]*Command
	builder       func(ParsedCommandInput) CommandParseResult
	commandAction func(context.Context, NamedCommandInvocation, CliContext) error
}

func NewCommand(name string) *Command {
	return &Command{Name: name, options: make(map[string]CommandOption), subcommands: make(map[string]*Command)}
}

func (c *Command) Option(option CommandOption) error {
	if _, exists := c.options[option.Name]; exists {
		return fmt.Errorf("Option %s is already registered for %s", option.Name, c.Name)
	}
	c.options[option.Name] = option
	return nil
}

func (c *Command) Build(builder func(ParsedCommandInput) CommandParseResult) *Command {
	c.builder = builder
	return c
}

func (c *Command) Action(action func(context.Context, NamedCommandInvocation, CliContext) error) *Command {
	c.commandAction = action
	return c
}

func (c *Command) Command(command *Command) error {
	if _, exists := c.subcommands[command.Name]; exists {
		return fmt.Errorf("Command %s is already registered", command.Name)
	}
	c.subcommands[command.Name] = command
	return nil
}

func (c *Command) Parse(argv []string) (CommandParseResult, error) {
	if selected := c.selectCommand(argv); selected != nil {
		return selected.Parse(argv[1:])
	}
	return c.parseOwn(argv)
}

// Execute waits for the selected action and returns its errors on the same operation. Invalid input never invokes an action.
func (c *Command) Execute(ctx context.Context, argv []string, commandContext CliContext) (CommandExecutionResult, error) {
	if selected := c.selectCommand(argv); selected != nil {
		return selected.Execute(ctx, argv[1:], commandContext)
	}
	parsed, err := c.parseOwn(argv)
	if err != nil || !parsed.Ok {
		return parsed, err
	}
	if c.commandAction == nil {
		return CommandExecutionResult{}, fmt.Errorf("Command %s does not define an action", c.Name)
	}
	if err := c.commandAction(ctx, parsed.Command, commandContext); err != nil {
		return CommandExecutionResult{}, err
	}
	return parsed, nil
}

func (c *Command) selectCommand(argv []string) *Command {
	if len(argv) == 0 {
		return nil
	}
	return c.subcommands[argv[0]]
}

func (c *Command) parseOwn(argv []string) (CommandParseResult, error) {
	if c.builder == nil {
		return CommandParseResult{}, fmt.Errorf("Command %s does not define a builder", c.Name)
	}
	input, diagnostics := c.parseOptions(argv)
	built := c.builder(input)
	if !built.Ok {
		diagnostics = append(diagnostics, built.Errors...)
	}
	if len(diagnostics) > 0 {
		return CommandParseResult{Errors: diagnostics}, nil
	}
	if !built.Ok {
		return CommandParseResult{}, fmt.Errorf("Command %s failed without an error", c.Name)
	}
	return CommandParseResult{Ok: true, Command: built.Command}, nil
}

func (c *Command) parseOptions(argv []string) (ParsedCommandInput, []string) {
	parsed := ParsedCommandInput{values: make(map[string][]any)}
	var diagnostics []string
	for index := 0; index < len(argv); index++ {
		argument := argv[index]
		name, candidate, equals := strings.Cut(argument, "=")
		option, exists := c.options[name]
		if argument == "--" || !exists {
			parsed.RemainingArgs = append(parsed.RemainingArgs, argv[index:]...)
			break
		}
		if option.Flag {
			if equals {
				diagnostics = append(diagnostics, name+" does not take a value")
				continue
			}
			candidate = ""
		} else {
			if !equals && index+1 < len(argv) && !strings.HasPrefix(argv[index+1], "-") {
				index++
				candidate = argv[index]
			}
			if candidate == "" {
				diagnostics = append(diagnostics, name+" requires a value")
				continue
			}
		}
		values := parsed.values[name]
		if len(values) > 0 && !option.Repeatable {
			diagnostics = append(diagnostics, name+" may only be specified once")
			continue
		}
		value, err := option.Parse(candidate)
		if err != nil {
			diagnostics = append(diagnostics, err.Error())
			continue
		}
		parsed.values[name] = append(values, value)
	}
	return parsed, diagnostics
}

// Ports packages/coding-agent/src/cli/experimental/command-options.ts

// pig divergence (D64): experimental Radius and its credential options are designed out; only Unix connection addresses are selectable.
var connectOption = ValueOption("--connect", func(value string) (any, error) { return parseTransportAddress(value) }, false)

func unsupportedOptions(command string, input ParsedCommandInput) []string {
	if len(input.RemainingArgs) == 0 {
		return nil
	}
	return []string{"The experimental " + command + " command does not support existing CLI options yet"}
}

func commandString(input ParsedCommandInput, option CommandOption) *string {
	value, ok := input.Value(option).(string)
	if !ok {
		return nil
	}
	return &value
}

func commandStrings(input ParsedCommandInput, option CommandOption) []string {
	var result []string
	for _, value := range input.Values(option) {
		result = append(result, value.(string))
	}
	return result
}

func commandFlag(input ParsedCommandInput, options ...CommandOption) *bool {
	for _, option := range options {
		if input.Value(option) == true {
			return new(true)
		}
	}
	return nil
}

// The accepted addresses must equal their WHATWG serialization. Non-special schemes preserve host case and path backslashes, unlike file and HTTP URLs.
func parseTransportAddress(value string) (*TransportAddress, error) {
	invalid := func() (*TransportAddress, error) {
		return nil, fmt.Errorf("Invalid --connect address \"%s\"", value)
	}
	input := strings.TrimFunc(value, func(r rune) bool { return r <= 0x20 })
	input = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, input)
	scheme, rest, hasScheme := strings.Cut(input, ":")
	if !hasScheme || !commandURLScheme(scheme) {
		return invalid()
	}
	scheme = strings.ToLower(scheme)
	// pig divergence (D64): Radius is not an experimental transport; use the normal unsupported-scheme diagnostic.
	if scheme != "unix" {
		if !validUnsupportedURL(scheme, rest) {
			return invalid()
		}
		return nil, fmt.Errorf("Unsupported --connect transport \"%s:\"", scheme)
	}
	host, credentials, port, pathname, valid := commandURLAuthority(rest)
	if !valid {
		return invalid()
	}
	if host != "" || credentials || port {
		return nil, errors.New("Unix transport address must not include an authority")
	}
	if input != value || !strings.HasPrefix(value, "unix:///") || strings.HasPrefix(value, "unix:////") || strings.ContainsAny(value, "?#") || !canonicalCommandURLPath(pathname) {
		return invalid()
	}
	path, err := url.PathUnescape(pathname)
	if err != nil || !utf8.ValidString(path) || strings.ContainsRune(path, 0) {
		return invalid()
	}
	if !strings.HasPrefix(path, "/") {
		return nil, errors.New("Unix transport address requires an absolute path")
	}
	return &TransportAddress{Transport: "unix", Path: path}, nil
}

func commandURLScheme(scheme string) bool {
	if scheme == "" {
		return false
	}
	for i := range len(scheme) {
		c := scheme[i] | 0x20
		if c >= 'a' && c <= 'z' {
			continue
		}
		if i == 0 || !strings.ContainsRune("0123456789+-.", rune(scheme[i])) {
			return false
		}
	}
	return true
}

func commandURLAuthority(rest string) (host string, credentials, port bool, pathname string, valid bool) {
	if !strings.HasPrefix(rest, "//") {
		return "", false, false, rest, true
	}
	rest = rest[2:]
	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}
	host, pathname = rest[:end], rest[end:]
	if at := strings.LastIndexByte(host, '@'); at >= 0 {
		credentials = host[:at] != "" && host[:at] != ":"
		host = host[at+1:]
		if host == "" {
			return "", false, false, "", false
		}
	}
	var portValue string
	if strings.HasPrefix(host, "[") {
		end := strings.IndexByte(host, ']')
		if end < 0 {
			return "", false, false, "", false
		}
		if end+1 < len(host) {
			if host[end+1] != ':' {
				return "", false, false, "", false
			}
			portValue = host[end+2:]
		}
		host = host[:end+1]
		var err error
		host, err = nodeurl.FileHost(host)
		if err != nil {
			return "", false, false, "", false
		}
	} else {
		var hasPort bool
		host, portValue, hasPort = strings.Cut(host, ":")
		if hasPort && host == "" {
			return "", false, false, "", false
		}
		if strings.ContainsAny(host, "\x00\t\n\r #/:<>?@[\\]^|") {
			return "", false, false, "", false
		}
		host = encodeOpaqueCommandHost(host)
	}
	if portValue != "" {
		if strings.Trim(portValue, "0123456789") != "" {
			return "", false, false, "", false
		}
		if _, err := strconv.ParseUint(portValue, 10, 16); err != nil {
			return "", false, false, "", false
		}
		port = true
	}
	return host, credentials, port, pathname, true
}

func encodeOpaqueCommandHost(host string) string {
	var result strings.Builder
	for i := range len(host) {
		c := host[i]
		if c <= 0x20 || c >= 0x7f {
			const hex = "0123456789ABCDEF"
			result.WriteByte('%')
			result.WriteByte(hex[c>>4])
			result.WriteByte(hex[c&15])
		} else {
			result.WriteByte(c)
		}
	}
	return result.String()
}

func canonicalCommandURLPath(pathname string) bool {
	if strings.ContainsFunc(pathname, func(r rune) bool { return r <= 0x20 || r >= 0x7f || strings.ContainsRune("\"<>^`{}", r) }) {
		return false
	}
	for segment := range strings.SplitSeq(pathname, "/") {
		switch strings.ToLower(segment) {
		case ".", "..", "%2e", ".%2e", "%2e.", "%2e%2e":
			return false
		}
	}
	return true
}

func validUnsupportedURL(scheme, rest string) bool {
	switch scheme {
	case "http", "https", "ws", "wss", "ftp":
		rest = strings.ReplaceAll(rest, "\\", "/")
		rest = "//" + strings.TrimLeft(rest, "/")
		host, _, _, _, valid := commandURLAuthority(rest)
		if !valid || host == "" {
			return false
		}
		_, err := nodeurl.FileHost(host)
		return err == nil
	case "file":
		_, err := nodeurl.FileURLToPath(scheme+":"+rest, false)
		if err == nil {
			return true
		}
		var urlError *nodeurl.Error
		return errors.As(err, &urlError) && urlError.Code != "ERR_INVALID_URL"
	default:
		_, _, _, _, valid := commandURLAuthority(rest)
		return valid
	}
}
