package main

import (
	"reflect"
	"testing"
)

// Pi's args.ts:147-156 assigns a new list on every --tools/-t and --exclude-tools/-xt: args[++i].split(",").map((s) => s.trim()).filter((name) => name.length > 0). The last occurrence wins, an empty result is still a defined list, and trim is String.prototype.trim.
func TestParseToolListFlagsLikePi(t *testing.T) {
	for _, tc := range []struct {
		name          string
		args          []string
		tools, xtools []string
	}{
		{name: "absent", args: nil},
		{name: "empty tools is an empty list", args: []string{"--tools", ""}, tools: []string{}},
		{name: "separators only", args: []string{"-t", " , ,"}, tools: []string{}},
		{name: "last tools wins", args: []string{"--tools", "read,bash", "-t", "ext_a"}, tools: []string{"ext_a"}},
		{name: "empty last tools replaces earlier list", args: []string{"--tools", "read", "--tools", ""}, tools: []string{}},
		{name: "last exclusion wins", args: []string{"--exclude-tools", "read", "-xt", "bash,edit"}, xtools: []string{"bash", "edit"}},
		{name: "empty exclusion is an empty list", args: []string{"-xt", ""}, xtools: []string{}},
		{name: "ECMAScript trim", args: []string{"--tools", "\ufeffread\ufeff,\u00a0bash\u3000", "-xt", "\u2028edit\u0085"}, tools: []string{"read", "bash"}, xtools: []string{"edit\u0085"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flags := parseFlags(tc.args)
			if !reflect.DeepEqual(flags.Tools, tc.tools) {
				t.Errorf("Tools = %#v, want %#v", flags.Tools, tc.tools)
			}
			if !reflect.DeepEqual(flags.ExcludeTools, tc.xtools) {
				t.Errorf("ExcludeTools = %#v, want %#v", flags.ExcludeTools, tc.xtools)
			}
		})
	}
}

// An explicit empty --tools list is Pi's allowedToolNames = [] (sdk.ts:260): the registry pi.getAllTools() reports is empty.
func TestToolRegistryFiltersKeepExplicitEmptyAllowlist(t *testing.T) {
	allowed, excluded := toolRegistryFilters(parseFlags([]string{"--no-builtin-tools", "--tools", ""}))
	if allowed == nil || len(allowed) != 0 {
		t.Fatalf("allowed = %#v, want an empty non-nil allowlist", allowed)
	}
	if excluded != nil {
		t.Fatalf("excluded = %#v, want nil", excluded)
	}
}
