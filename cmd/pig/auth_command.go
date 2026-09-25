package main

// Mirrors upstream packages/coding-agent/src/cli/auth-command.ts: parsing,
// usage, and argument validation for `pig auth check`, `pig auth
// print-api-key`, and `pig auth print-bearer-token`.

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// AuthCommandKind mirrors upstream AuthCommandKind.
type AuthCommandKind string

const (
	AuthCommandCheck       AuthCommandKind = "check"
	AuthCommandAPIKey      AuthCommandKind = "api_key"
	AuthCommandBearerToken AuthCommandKind = "bearer_token"
)

// AuthCommand mirrors upstream AuthCommand.
type AuthCommand struct {
	Kind        AuthCommandKind
	Args        []string
	JSON        bool
	Credentials bool
	NoRefresh   bool
	// MinExpiryMs is set only by print-bearer-token --min-expiry.
	MinExpiryMs *float64
}

// AuthCommandError is a user-facing auth command failure.
type AuthCommandError struct{ Message string }

func (e *AuthCommandError) Error() string { return e.Message }

func authCommandError(format string, args ...any) *AuthCommandError {
	return &AuthCommandError{Message: fmt.Sprintf(format, args...)}
}

var authCommandUsage = map[AuthCommandKind]string{
	AuthCommandCheck:       codingagent.AppName + " auth check --provider <provider> [--json] [--credentials] [--no-refresh]",
	AuthCommandAPIKey:      codingagent.AppName + " auth print-api-key --provider <provider> [--model <model>]",
	AuthCommandBearerToken: codingagent.AppName + " auth print-bearer-token --provider <provider> [--model <model>] [--min-expiry <duration>]",
}

// GetAuthCommandName mirrors upstream getAuthCommandName.
func GetAuthCommandName(kind AuthCommandKind) string {
	switch kind {
	case AuthCommandCheck:
		return "auth check"
	case AuthCommandAPIKey:
		return "auth print-api-key"
	default:
		return "auth print-bearer-token"
	}
}

// GetAuthCommandUsage mirrors upstream getAuthCommandUsage.
func GetAuthCommandUsage(kind AuthCommandKind) string {
	return authCommandUsage[kind]
}

// IsAuthCommandHelp mirrors upstream isAuthCommandHelp.
func IsAuthCommandHelp(args []string) bool {
	if len(args) == 0 || args[0] != "auth" {
		return false
	}
	return len(args) < 2 || args[1] == "help" || slices.Contains(args, "--help") || slices.Contains(args, "-h")
}

// authCommandHelp is upstream printAuthCommandHelp's text. Upstream prints
// the literal `pi` here rather than APP_NAME.
const authCommandHelp = `Usage:
  pi auth print-api-key [--provider <provider>] [--model <model>]
  pi auth print-bearer-token [--provider <provider>] [--model <model>] [--min-expiry <duration>]
  pi auth check [--provider <provider>] [--model <model>] [--json] [--credentials] [--no-refresh]

Auth commands require at least one of --provider or --model. Checks refresh expired OAuth credentials by default; --no-refresh prevents this. --credentials emits the credential, or includes it in JSON output.`

var minExpiryPattern = regexp.MustCompile(`(?i)^(\d+)(ms|s|m|h)$`)

// ParseAuthCommand mirrors upstream parseAuthCommand. It returns nil for
// arguments that are not an auth command.
func ParseAuthCommand(args []string) (*AuthCommand, error) {
	if len(args) == 0 || args[0] != "auth" {
		return nil, nil
	}
	var kind AuthCommandKind
	subcommand := ""
	if len(args) > 1 {
		subcommand = args[1]
	}
	switch subcommand {
	case "check":
		kind = AuthCommandCheck
	case "print-api-key":
		kind = AuthCommandAPIKey
	case "print-bearer-token":
		kind = AuthCommandBearerToken
	default:
		return nil, authCommandError(`Unknown auth command "%s". Use "%s auth print-api-key", "%s auth print-bearer-token", or "%s auth check".`, subcommand, codingagent.AppName, codingagent.AppName, codingagent.AppName)
	}
	command := &AuthCommand{Kind: kind, Args: []string{}}
	for index := 2; index < len(args); index++ {
		arg := args[index]
		if arg == "--min-expiry" {
			if kind != AuthCommandBearerToken {
				return nil, authCommandError("--min-expiry is only supported by print-bearer-token")
			}
			index++
			value := ""
			if index < len(args) {
				value = args[index]
			}
			minExpiryMs, ok := parseMinExpiry(value)
			if !ok {
				return nil, authCommandError("--min-expiry must use a duration such as 30m or 1h")
			}
			command.MinExpiryMs = &minExpiryMs
			continue
		}
		if arg == "--json" || arg == "--credentials" || arg == "--no-refresh" {
			if kind != AuthCommandCheck {
				return nil, authCommandError("%s is only supported by auth check", arg)
			}
			switch arg {
			case "--json":
				command.JSON = true
			case "--credentials":
				command.Credentials = true
			default:
				command.NoRefresh = true
			}
			continue
		}
		command.Args = append(command.Args, arg)
	}
	return command, nil
}

func parseMinExpiry(value string) (float64, bool) {
	match := minExpiryPattern.FindStringSubmatch(value)
	if match == nil {
		return 0, false
	}
	amount, _ := strconv.ParseFloat(match[1], 64) // Digit-only input; range overflow is +Inf, as with Number().
	switch match[2] {
	case "ms":
		return amount, true
	case "s":
		return amount * 1_000, true
	case "m":
		return amount * 60_000, true
	default:
		// Upstream compares the unit case-sensitively, so any other
		// accepted spelling (h, and upper-case units) is hours.
		return amount * 3_600_000, true
	}
}

// AuthCommandTarget is the provider/model pair an auth command resolves.
type AuthCommandTarget struct {
	Provider string
	Model    string
}

// ValidateAuthCommandArgs mirrors upstream validateAuthCommandArgs. rawArgs
// are the arguments parsed into flags, used to report the first unknown
// option in argument order.
func ValidateAuthCommandArgs(flags CLIFlags, rawArgs []string, kind AuthCommandKind) (AuthCommandTarget, error) {
	target := AuthCommandTarget{Provider: strings.TrimSpace(flags.Provider), Model: strings.TrimSpace(flags.Model)}
	if option, ok := firstUnknownFlag(flags, rawArgs); ok {
		return AuthCommandTarget{}, authCommandError(`Unknown option --%s for "%s".`, option, GetAuthCommandName(kind))
	}
	if apiKeyFlagSet(rawArgs) || len(flags.Args) > 0 || len(flags.FileArgs) > 0 {
		return AuthCommandTarget{}, authCommandError("Auth commands only accept --provider and --model")
	}
	if target.Provider == "" && target.Model == "" {
		if kind == AuthCommandCheck {
			return AuthCommandTarget{}, authCommandError("Auth checks require --provider <provider> or --model <model>")
		}
		return AuthCommandTarget{}, authCommandError("Credential printing requires --provider <provider> or --model <model>")
	}
	return target, nil
}

// firstUnknownFlag returns the first unknown option in argument order, as
// upstream reads the first key of its insertion-ordered unknownFlags map.
func firstUnknownFlag(flags CLIFlags, rawArgs []string) (string, bool) {
	if len(flags.UnknownFlags) == 0 {
		return "", false
	}
	for _, arg := range rawArgs {
		name, ok := strings.CutPrefix(arg, "--")
		if !ok {
			continue
		}
		name, _, _ = strings.Cut(name, "=")
		if _, unknown := flags.UnknownFlags[name]; unknown {
			return name, true
		}
	}
	for name := range flags.UnknownFlags {
		return name, true
	}
	return "", false
}

// apiKeyFlagSet reports whether parseFlags consumed an --api-key value, which
// upstream detects as args.apiKey !== undefined even when the value is empty.
func apiKeyFlagSet(rawArgs []string) bool {
	for index, arg := range rawArgs {
		if arg == "--api-key" && index+1 < len(rawArgs) {
			return true
		}
	}
	return false
}

// authorizationBearerPattern mirrors upstream /^Bearer\s+(.+)$/iu with
// JavaScript's \s and line-terminator-free `.`.
var authorizationBearerPattern = regexp.MustCompile(`(?i)^Bearer[\t\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+([^\n\r\x{2028}\x{2029}]+)$`)

// GetAuthCredential mirrors upstream getAuthCredential: the resolved API key,
// otherwise the token of an `Authorization: Bearer` header.
func GetAuthCredential(auth *ai.AuthResult) string {
	if auth == nil {
		return ""
	}
	if auth.Auth.APIKey != "" {
		return auth.Auth.APIKey
	}
	for name, value := range auth.Auth.Headers {
		if !strings.EqualFold(name, "authorization") {
			continue
		}
		if value == nil {
			return ""
		}
		if match := authorizationBearerPattern.FindStringSubmatch(*value); match != nil {
			return match[1]
		}
		return ""
	}
	return ""
}
