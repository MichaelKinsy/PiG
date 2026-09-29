package codingagent

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Each subtest ports all assertions at the cited upstream case site.
func TestUpstreamPromptTemplatesArguments(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:28
	t.Run("substituteArgs/should replace $ARGUMENTS with all args joined", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("Test: $ARGUMENTS", []string{"a", "b", "c"}), "Test: a b c"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:32
	t.Run("substituteArgs/should replace $@ with all args joined", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("Test: $@", []string{"a", "b", "c"}), "Test: a b c"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:36
	t.Run("substituteArgs/should replace $@ and $ARGUMENTS identically", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("Test: $@", []string{"foo", "bar", "baz"}), SubstitutePromptArgs("Test: $ARGUMENTS", []string{"foo", "bar", "baz"}); got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:42
	t.Run("substituteArgs/should NOT recursively substitute patterns in argument values", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("$ARGUMENTS", []string{"$1", "$ARGUMENTS"}), "$1 $ARGUMENTS"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
		if got, want := SubstitutePromptArgs("$@", []string{"$100", "$1"}), "$100 $1"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
		if got, want := SubstitutePromptArgs("$ARGUMENTS", []string{"$100", "$1"}), "$100 $1"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:48
	t.Run("substituteArgs/should support mixed $1, $2, and $ARGUMENTS", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("$1: $ARGUMENTS", []string{"prefix", "a", "b"}), "prefix: prefix a b"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:52
	t.Run("substituteArgs/should support mixed $1, $2, and $@", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("$1: $@", []string{"prefix", "a", "b"}), "prefix: prefix a b"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:56
	t.Run("substituteArgs/should handle empty arguments array with $ARGUMENTS", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("Test: $ARGUMENTS", []string{}), "Test: "; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:60
	t.Run("substituteArgs/should handle empty arguments array with $@", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("Test: $@", []string{}), "Test: "; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:64
	t.Run("substituteArgs/should handle empty arguments array with $1", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("Test: $1", []string{}), "Test: "; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:68
	t.Run("substituteArgs/should handle multiple occurrences of $ARGUMENTS", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("$ARGUMENTS and $ARGUMENTS", []string{"a", "b"}), "a b and a b"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:72
	t.Run("substituteArgs/should handle multiple occurrences of $@", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("$@ and $@", []string{"a", "b"}), "a b and a b"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:76
	t.Run("substituteArgs/should handle mixed occurrences of $@ and $ARGUMENTS", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("$@ and $ARGUMENTS", []string{"a", "b"}), "a b and a b"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:80
	t.Run("substituteArgs/should handle special characters in arguments", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("$1 $2: $ARGUMENTS", []string{"arg100", "@user"}), "arg100 @user: arg100 @user"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:85
	t.Run("substituteArgs/should handle out-of-range numbered placeholders", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("$1 $2 $3 $4 $5", []string{"a", "b"}), "a b   "; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:90
	t.Run("substituteArgs/should handle unicode characters", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("$ARGUMENTS", []string{"日本語", "🎉", "café"}), "日本語 🎉 café"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:94
	t.Run("substituteArgs/should preserve newlines and tabs in argument values", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("$1 $2", []string{"line1\nline2", "tab\tthere"}), "line1\nline2 tab\tthere"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:98
	t.Run("substituteArgs/should handle consecutive dollar patterns", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("$1$2", []string{"a", "b"}), "ab"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:102
	t.Run("substituteArgs/should handle quoted arguments with spaces", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("$ARGUMENTS", []string{"first arg", "second arg"}), "first arg second arg"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:106
	t.Run("substituteArgs/should handle single argument with $ARGUMENTS", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("Test: $ARGUMENTS", []string{"only"}), "Test: only"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:110
	t.Run("substituteArgs/should handle single argument with $@", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("Test: $@", []string{"only"}), "Test: only"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:114
	t.Run("substituteArgs/should handle $0 (zero index)", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("$0", []string{"a", "b"}), ""; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:118
	t.Run("substituteArgs/should handle decimal number in pattern (only integer part matches)", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("$1.5", []string{"a"}), "a.5"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:122
	t.Run("substituteArgs/should handle $ARGUMENTS as part of word", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("pre$ARGUMENTS", []string{"a", "b"}), "prea b"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:126
	t.Run("substituteArgs/should handle $@ as part of word", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("pre$@", []string{"a", "b"}), "prea b"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:130
	t.Run("substituteArgs/should handle empty arguments in middle of list", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("$ARGUMENTS", []string{"a", "", "c"}), "a  c"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:134
	t.Run("substituteArgs/should handle trailing and leading spaces in arguments", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("$ARGUMENTS", []string{"  leading  ", "trailing  "}), "  leading   trailing  "; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:138
	t.Run("substituteArgs/should handle argument containing pattern partially", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("Prefix $ARGUMENTS suffix", []string{"ARGUMENTS"}), "Prefix ARGUMENTS suffix"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:142
	t.Run("substituteArgs/should handle non-matching patterns", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("$A $$ $ $ARGS", []string{"a"}), "$A $$ $ $ARGS"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:146
	t.Run("substituteArgs/should handle case variations (case-sensitive)", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("$arguments $Arguments $ARGUMENTS", []string{"a", "b"}), "$arguments $Arguments a b"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:150
	t.Run("substituteArgs/should handle both syntaxes in same command with same result", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("$@ and $ARGUMENTS", []string{"x", "y", "z"}), SubstitutePromptArgs("$ARGUMENTS and $@", []string{"x", "y", "z"}); got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
		if got, want := SubstitutePromptArgs("$@ and $ARGUMENTS", []string{"x", "y", "z"}), "x y z and x y z"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:158
	t.Run("substituteArgs/should handle very long argument lists", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("$ARGUMENTS", []string{"arg0", "arg1", "arg2", "arg3", "arg4", "arg5", "arg6", "arg7", "arg8", "arg9", "arg10", "arg11", "arg12", "arg13", "arg14", "arg15", "arg16", "arg17", "arg18", "arg19", "arg20", "arg21", "arg22", "arg23", "arg24", "arg25", "arg26", "arg27", "arg28", "arg29", "arg30", "arg31", "arg32", "arg33", "arg34", "arg35", "arg36", "arg37", "arg38", "arg39", "arg40", "arg41", "arg42", "arg43", "arg44", "arg45", "arg46", "arg47", "arg48", "arg49", "arg50", "arg51", "arg52", "arg53", "arg54", "arg55", "arg56", "arg57", "arg58", "arg59", "arg60", "arg61", "arg62", "arg63", "arg64", "arg65", "arg66", "arg67", "arg68", "arg69", "arg70", "arg71", "arg72", "arg73", "arg74", "arg75", "arg76", "arg77", "arg78", "arg79", "arg80", "arg81", "arg82", "arg83", "arg84", "arg85", "arg86", "arg87", "arg88", "arg89", "arg90", "arg91", "arg92", "arg93", "arg94", "arg95", "arg96", "arg97", "arg98", "arg99"}), "arg0 arg1 arg2 arg3 arg4 arg5 arg6 arg7 arg8 arg9 arg10 arg11 arg12 arg13 arg14 arg15 arg16 arg17 arg18 arg19 arg20 arg21 arg22 arg23 arg24 arg25 arg26 arg27 arg28 arg29 arg30 arg31 arg32 arg33 arg34 arg35 arg36 arg37 arg38 arg39 arg40 arg41 arg42 arg43 arg44 arg45 arg46 arg47 arg48 arg49 arg50 arg51 arg52 arg53 arg54 arg55 arg56 arg57 arg58 arg59 arg60 arg61 arg62 arg63 arg64 arg65 arg66 arg67 arg68 arg69 arg70 arg71 arg72 arg73 arg74 arg75 arg76 arg77 arg78 arg79 arg80 arg81 arg82 arg83 arg84 arg85 arg86 arg87 arg88 arg89 arg90 arg91 arg92 arg93 arg94 arg95 arg96 arg97 arg98 arg99"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:164
	t.Run("substituteArgs/should handle numbered placeholders with single digit", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("$1 $2 $3", []string{"a", "b", "c"}), "a b c"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:168
	t.Run("substituteArgs/should handle numbered placeholders with multiple digits", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("$10 $12 $15", []string{"val0", "val1", "val2", "val3", "val4", "val5", "val6", "val7", "val8", "val9", "val10", "val11", "val12", "val13", "val14"}), "val9 val11 val14"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:173
	t.Run("substituteArgs/should handle escaped dollar signs (literal backslash preserved)", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("Price: \\$100", []string{}), "Price: \\"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:178
	t.Run("substituteArgs/should handle mixed numbered and wildcard placeholders", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("$1: $@ ($ARGUMENTS)", []string{"first", "second", "third"}), "first: first second third (first second third)"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:184
	t.Run("substituteArgs/should handle command with no placeholders", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("Just plain text", []string{"a", "b"}), "Just plain text"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:188
	t.Run("substituteArgs/should handle command with only placeholders", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("$1 $2 $@", []string{"a", "b", "c"}), "a b a b c"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:198
	t.Run("substituteArgs - positional defaults/should use default when positional arg is missing", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("List exactly ${1:-7} next steps", []string{}), "List exactly 7 next steps"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:202
	t.Run("substituteArgs - positional defaults/should support defaults for all arguments", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("${@:-default}\n${ARGUMENTS:-default}", []string{}), "default\ndefault"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
		if got, want := SubstitutePromptArgs("${@:-default}\n${ARGUMENTS:-default}", []string{"This", "would", "be", "the", "arguments"}), "This would be the arguments\nThis would be the arguments"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:211
	t.Run("substituteArgs - positional defaults/should use positional arg when present", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("List exactly ${1:-7} next steps", []string{"3"}), "List exactly 3 next steps"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:215
	t.Run("substituteArgs - positional defaults/should use default when positional arg is empty", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("Mode: ${1:-brief}", []string{""}), "Mode: brief"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:219
	t.Run("substituteArgs - positional defaults/should support multiple positional defaults", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("${1:-7} ${2:-brief}", []string{}), "7 brief"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
		if got, want := SubstitutePromptArgs("${1:-7} ${2:-brief}", []string{"3"}), "3 brief"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
		if got, want := SubstitutePromptArgs("${1:-7} ${2:-brief}", []string{"3", "verbose"}), "3 verbose"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:225
	t.Run("substituteArgs - positional defaults/should not recursively substitute patterns in arg values", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("${1:-7}", []string{"$ARGUMENTS"}), "$ARGUMENTS"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
		if got, want := SubstitutePromptArgs("${1:-7}", []string{"$1"}), "$1"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:230
	t.Run("substituteArgs - positional defaults/should not recursively substitute patterns in default values", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("${1:-$ARGUMENTS}", []string{"a", "b"}), "a"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
		if got, want := SubstitutePromptArgs("${3:-$ARGUMENTS}", []string{"a", "b"}), "$ARGUMENTS"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:235
	t.Run("substituteArgs - positional defaults/should support defaults with spaces", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("${1:-seven steps}", []string{}), "seven steps"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:239
	t.Run("substituteArgs - positional defaults/should support out-of-range positional defaults", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("${3:-fallback}", []string{"a", "b"}), "fallback"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:243
	t.Run("substituteArgs - positional defaults/should mix positional defaults with existing placeholders", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("$1 ${2:-x} $ARGUMENTS", []string{"a"}), "a x a"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:253
	t.Run("substituteArgs - array slicing/should slice from index (${@:N})", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("${@:2}", []string{"a", "b", "c", "d"}), "b c d"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
		if got, want := SubstitutePromptArgs("${@:1}", []string{"a", "b", "c"}), "a b c"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
		if got, want := SubstitutePromptArgs("${@:3}", []string{"a", "b", "c", "d"}), "c d"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:259
	t.Run("substituteArgs - array slicing/should slice with length (${@:N:L})", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("${@:2:2}", []string{"a", "b", "c", "d"}), "b c"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
		if got, want := SubstitutePromptArgs("${@:1:1}", []string{"a", "b", "c"}), "a"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
		if got, want := SubstitutePromptArgs("${@:3:1}", []string{"a", "b", "c", "d"}), "c"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
		if got, want := SubstitutePromptArgs("${@:2:3}", []string{"a", "b", "c", "d", "e"}), "b c d"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:266
	t.Run("substituteArgs - array slicing/should handle out of range slices", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("${@:99}", []string{"a", "b"}), ""; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
		if got, want := SubstitutePromptArgs("${@:5}", []string{"a", "b"}), ""; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
		if got, want := SubstitutePromptArgs("${@:10:5}", []string{"a", "b"}), ""; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:272
	t.Run("substituteArgs - array slicing/should handle zero-length slices", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("${@:2:0}", []string{"a", "b", "c"}), ""; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
		if got, want := SubstitutePromptArgs("${@:1:0}", []string{"a", "b"}), ""; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:277
	t.Run("substituteArgs - array slicing/should handle length exceeding array", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("${@:2:99}", []string{"a", "b", "c"}), "b c"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
		if got, want := SubstitutePromptArgs("${@:1:10}", []string{"a", "b"}), "a b"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:282
	t.Run("substituteArgs - array slicing/should process slice before simple $@", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("${@:2} vs $@", []string{"a", "b", "c"}), "b c vs a b c"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
		if got, want := SubstitutePromptArgs("First: ${@:1:1}, All: $@", []string{"x", "y", "z"}), "First: x, All: x y z"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:287
	t.Run("substituteArgs - array slicing/should not recursively substitute slice patterns in args", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("${@:1}", []string{"${@:2}", "test"}), "${@:2} test"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
		if got, want := SubstitutePromptArgs("${@:2}", []string{"a", "${@:3}", "c"}), "${@:3} c"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:292
	t.Run("substituteArgs - array slicing/should handle mixed usage with positional args", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("$1: ${@:2}", []string{"cmd", "arg1", "arg2"}), "cmd: arg1 arg2"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
		if got, want := SubstitutePromptArgs("$1 $2 ${@:3}", []string{"a", "b", "c", "d"}), "a b c d"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:297
	t.Run("substituteArgs - array slicing/should treat ${@:0} as all args", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("${@:0}", []string{"a", "b", "c"}), "a b c"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:301
	t.Run("substituteArgs - array slicing/should handle empty args array", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("${@:2}", []string{}), ""; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
		if got, want := SubstitutePromptArgs("${@:1}", []string{}), ""; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:306
	t.Run("substituteArgs - array slicing/should handle single arg array", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("${@:1}", []string{"only"}), "only"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
		if got, want := SubstitutePromptArgs("${@:2}", []string{"only"}), ""; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:311
	t.Run("substituteArgs - array slicing/should handle slice in middle of text", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("Process ${@:2} with $1", []string{"tool", "file1", "file2"}), "Process file1 file2 with tool"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:317
	t.Run("substituteArgs - array slicing/should handle multiple slices in one template", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("${@:1:1} and ${@:2}", []string{"a", "b", "c"}), "a and b c"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
		if got, want := SubstitutePromptArgs("${@:1:2} vs ${@:3:2}", []string{"a", "b", "c", "d", "e"}), "a b vs c d"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:322
	t.Run("substituteArgs - array slicing/should handle quoted arguments in slices", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("${@:2}", []string{"cmd", "first arg", "second arg"}), "first arg second arg"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:326
	t.Run("substituteArgs - array slicing/should handle special characters in sliced args", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("${@:2}", []string{"cmd", "$100", "@user", "#tag"}), "$100 @user #tag"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:330
	t.Run("substituteArgs - array slicing/should handle unicode in sliced args", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("${@:1}", []string{"日本語", "🎉", "café"}), "日本語 🎉 café"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:334
	t.Run("substituteArgs - array slicing/should combine positional, slice, and wildcard placeholders", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("Run $1 on ${@:2:2}, then process $@", []string{"eslint", "file1.ts", "file2.ts", "file3.ts"}), "Run eslint on file1.ts file2.ts, then process eslint file1.ts file2.ts file3.ts"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:342
	t.Run("substituteArgs - array slicing/should handle slice with no spacing", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("prefix${@:2}suffix", []string{"a", "b", "c"}), "prefixb csuffix"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:346
	t.Run("substituteArgs - array slicing/should handle large slice lengths gracefully", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("${@:5:100}", []string{"arg1", "arg2", "arg3", "arg4", "arg5", "arg6", "arg7", "arg8", "arg9", "arg10"}), "arg5 arg6 arg7 arg8 arg9 arg10"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:357
	t.Run("parseCommandArgs/should parse simple space-separated arguments", func(t *testing.T) {
		if got, want := ParsePromptArgs("a b c"), []string{"a", "b", "c"}; !slices.Equal(got, want) {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:361
	t.Run("parseCommandArgs/should parse quoted arguments with spaces", func(t *testing.T) {
		if got, want := ParsePromptArgs("\"first arg\" second"), []string{"first arg", "second"}; !slices.Equal(got, want) {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:365
	t.Run("parseCommandArgs/should parse single-quoted arguments", func(t *testing.T) {
		if got, want := ParsePromptArgs("'first arg' second"), []string{"first arg", "second"}; !slices.Equal(got, want) {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:369
	t.Run("parseCommandArgs/should parse mixed quote styles", func(t *testing.T) {
		if got, want := ParsePromptArgs("\"double\" 'single' \"double again\""), []string{"double", "single", "double again"}; !slices.Equal(got, want) {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:373
	t.Run("parseCommandArgs/should handle empty string", func(t *testing.T) {
		if got, want := ParsePromptArgs(""), []string{}; !slices.Equal(got, want) {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:377
	t.Run("parseCommandArgs/should handle extra spaces", func(t *testing.T) {
		if got, want := ParsePromptArgs("a  b   c"), []string{"a", "b", "c"}; !slices.Equal(got, want) {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:381
	t.Run("parseCommandArgs/should handle tabs as separators", func(t *testing.T) {
		if got, want := ParsePromptArgs("a\tb\tc"), []string{"a", "b", "c"}; !slices.Equal(got, want) {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:385
	t.Run("parseCommandArgs/should handle quoted empty string", func(t *testing.T) {
		if got, want := ParsePromptArgs("\"\" \" \""), []string{" "}; !slices.Equal(got, want) {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:390
	t.Run("parseCommandArgs/should handle arguments with special characters", func(t *testing.T) {
		if got, want := ParsePromptArgs("$100 @user #tag"), []string{"$100", "@user", "#tag"}; !slices.Equal(got, want) {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:394
	t.Run("parseCommandArgs/should handle unicode characters", func(t *testing.T) {
		if got, want := ParsePromptArgs("日本語 🎉 café"), []string{"日本語", "🎉", "café"}; !slices.Equal(got, want) {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:398
	t.Run("parseCommandArgs/should handle newlines in quoted arguments", func(t *testing.T) {
		if got, want := ParsePromptArgs("\"line1\nline2\" second"), []string{"line1\nline2", "second"}; !slices.Equal(got, want) {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:402
	t.Run("parseCommandArgs/should treat unquoted newlines as separators", func(t *testing.T) {
		if got, want := ParsePromptArgs("label-2\n\nHere is some description #2."), []string{"label-2", "Here", "is", "some", "description", "#2."}; !slices.Equal(got, want) {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:413
	t.Run("parseCommandArgs/should collapse mixed unquoted whitespace", func(t *testing.T) {
		if got, want := ParsePromptArgs("a\n\n\tb  c"), []string{"a", "b", "c"}; !slices.Equal(got, want) {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:417
	t.Run("parseCommandArgs/should handle escaped quotes inside quoted strings", func(t *testing.T) {
		if got, want := ParsePromptArgs("\"quoted \\\"text\\\"\""), []string{"quoted \\text\\"}; !slices.Equal(got, want) {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:422
	t.Run("parseCommandArgs/should handle trailing spaces", func(t *testing.T) {
		if got, want := ParsePromptArgs("a b c   "), []string{"a", "b", "c"}; !slices.Equal(got, want) {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:426
	t.Run("parseCommandArgs/should handle leading spaces", func(t *testing.T) {
		if got, want := ParsePromptArgs("   a b c"), []string{"a", "b", "c"}; !slices.Equal(got, want) {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:436
	t.Run("expandPromptTemplate/should split template arguments on unquoted newlines", func(t *testing.T) {
		if got, want := upstreamExpandPrompt(t, "/arg-test label-2\n\nHere is some description #2.", []PromptTemplate{{Name: "arg-test", Description: "test", Content: "- arg1: $1\n- rest: ${@:2}", FilePath: "/tmp/arg-test.md"}}), "- arg1: label-2\n- rest: Here is some description #2."; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:450
	t.Run("expandPromptTemplate/should support template command separated from args by newline", func(t *testing.T) {
		if got, want := upstreamExpandPrompt(t, "/arg-test\nlabel-2", []PromptTemplate{{Name: "arg-test", Description: "test", Content: "arg1: $1", FilePath: "/tmp/arg-test.md"}}), "arg1: label-2"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:470
	t.Run("parseCommandArgs + substituteArgs integration/should parse and substitute together correctly", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("Create component $1 with features: $ARGUMENTS", ParsePromptArgs("Button \"onClick handler\" \"disabled support\"")), "Create component Button with features: Button onClick handler disabled support"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:478
	t.Run("parseCommandArgs + substituteArgs integration/should handle the example from README", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("Create a React component named $1 with features: $ARGUMENTS", ParsePromptArgs("Button \"onClick handler\" \"disabled support\"")), "Create a React component named Button with features: Button onClick handler disabled support"; got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:488
	t.Run("parseCommandArgs + substituteArgs integration/should produce same result with $@ and $ARGUMENTS", func(t *testing.T) {
		if got, want := SubstitutePromptArgs("Implement: $@", ParsePromptArgs("feature1 feature2 feature3")), SubstitutePromptArgs("Implement: $ARGUMENTS", ParsePromptArgs("feature1 feature2 feature3")); got != want {
			t.Fatalf("got %q; want %q", got, want)
		}
	})
}

func TestUpstreamPromptTemplateArgumentHints(t *testing.T) {
	for _, tc := range []struct{ name, filename, content, hint, description string }{
		// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:508
		{"should parse required argument-hint from frontmatter", "pr", "---\ndescription: Review PRs from URLs with structured issue and code analysis\nargument-hint: \"<PR-URL>\"\n---\nYou are given one or more GitHub PR URLs: $@", "<PR-URL>", "Review PRs from URLs with structured issue and code analysis"},
		// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:531
		{"should parse optional argument-hint from frontmatter", "wr", "---\ndescription: Finish the current task end-to-end with changelog, commit, and push\nargument-hint: \"[instructions]\"\n---\nWrap it. Additional instructions: $ARGUMENTS", "[instructions]", "Finish the current task end-to-end with changelog, commit, and push"},
		// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:554
		{"should leave argumentHint undefined when not specified", "cl", "---\ndescription: Audit changelog entries before release\n---\nAudit changelog entries for all commits since the last release.", "", "Audit changelog entries before release"},
		// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:575
		{"should ignore empty argument-hint", "empty-hint", "---\ndescription: A command with empty hint\nargument-hint: \"\"\n---\nDo something", "", "A command with empty hint"},
		// .upstream/v0.87.1/packages/coding-agent/test/prompt-templates.test.ts:597
		{"should preserve argument-hint with special characters", "is", "---\ndescription: Analyze GitHub issues (bugs or feature requests)\nargument-hint: \"<issue>\"\n---\nAnalyze GitHub issue(s): $ARGUMENTS", "<issue>", "Analyze GitHub issues (bugs or feature requests)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, tc.filename+".md"), []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			result := LoadPromptTemplates("", "", dir)
			if len(result.Templates) != 1 || len(result.Diagnostics) != 0 {
				t.Fatalf("load = %#v", result)
			}
			got := result.Templates[0]
			if got.Name != tc.filename || got.ArgumentHint != tc.hint || got.Description != tc.description {
				t.Fatalf("template = %#v; want name %q, hint %q, description %q", got, tc.filename, tc.hint, tc.description)
			}
		})
	}
}

func upstreamExpandPrompt(t *testing.T, input string, templates []PromptTemplate) string {
	t.Helper()
	output, ok := ExpandPromptTemplate(input, templates)
	if !ok {
		t.Fatalf("template did not expand: %q", input)
	}
	return output
}
