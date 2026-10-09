package cli

import "testing"

// TestParsePackageCommandFlagsMatchPi pins each package command's flags to Pi's parsePackageCommand
// (packages/coding-agent/src/package-manager-cli.ts:426-500): --help and the approval overrides on every command, --local on install and
// remove, and --self, --extensions, --models, --all, --force and --extension on update. A flag outside its command is an invalid option.
func TestParsePackageCommandFlagsMatchPi(t *testing.T) {
	yes, no := true, false
	type check func(o *packageCLIOptions) bool
	override := func(want *bool) check {
		return func(o *packageCLIOptions) bool {
			return o.projectTrustOverride != nil && *o.projectTrustOverride == *want
		}
	}
	cases := []struct {
		args    []string
		ok      check
		invalid string
	}{
		{args: []string{"install", "--help"}, ok: func(o *packageCLIOptions) bool { return o.help }},
		{args: []string{"install", "--local", "npm:x"}, ok: func(o *packageCLIOptions) bool { return o.local }},
		{args: []string{"install", "--approve", "npm:x"}, ok: override(&yes)},
		{args: []string{"install", "--no-approve", "npm:x"}, ok: override(&no)},
		{args: []string{"remove", "--help"}, ok: func(o *packageCLIOptions) bool { return o.help }},
		{args: []string{"remove", "--local", "npm:x"}, ok: func(o *packageCLIOptions) bool { return o.local }},
		{args: []string{"remove", "--approve", "npm:x"}, ok: override(&yes)},
		{args: []string{"remove", "--no-approve", "npm:x"}, ok: override(&no)},
		{args: []string{"list", "--help"}, ok: func(o *packageCLIOptions) bool { return o.help }},
		{args: []string{"list", "--approve"}, ok: override(&yes)},
		{args: []string{"list", "--no-approve"}, ok: override(&no)},
		{args: []string{"update", "--help"}, ok: func(o *packageCLIOptions) bool { return o.help }},
		{args: []string{"update", "--approve"}, ok: override(&yes)},
		{args: []string{"update", "--no-approve"}, ok: override(&no)},
		{args: []string{"update", "--all"}, ok: func(o *packageCLIOptions) bool { return o.allPackages }},
		{args: []string{"update", "--extensions"}, ok: func(o *packageCLIOptions) bool { return o.extensionsOnly }},
		{args: []string{"update", "--models"}, ok: func(o *packageCLIOptions) bool { return o.modelsOnly }},
		{args: []string{"update", "--self"}, ok: func(o *packageCLIOptions) bool { return o.selfOnly }},
		{args: []string{"update", "--force"}, ok: func(o *packageCLIOptions) bool { return o.force }},
		{args: []string{"update", "--extension", "npm:x"}, ok: func(o *packageCLIOptions) bool { return o.extensionSource == "npm:x" }},
		{args: []string{"list", "--local"}, invalid: "--local"},
		{args: []string{"update", "--local"}, invalid: "--local"},
		{args: []string{"install", "--all", "npm:x"}, invalid: "--all"},
		{args: []string{"remove", "--self", "npm:x"}, invalid: "--self"},
		{args: []string{"install", "--force", "npm:x"}, invalid: "--force"},
	}
	for _, tc := range cases {
		o, ok := parsePackageCommand(tc.args)
		if !ok {
			t.Errorf("%v: not a package command", tc.args)
			continue
		}
		switch {
		case tc.invalid != "":
			if o.invalidOption != tc.invalid {
				t.Errorf("%v: invalidOption %q, want %q", tc.args, o.invalidOption, tc.invalid)
			}
		case !tc.ok(o) || o.invalidOption != "":
			t.Errorf("%v: flag not applied: %+v", tc.args, *o)
		}
	}
}
