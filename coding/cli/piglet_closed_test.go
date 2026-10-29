package cli

import (
	"strings"
	"testing"
)

const (
	closedPigletYAML = "name: pig-tern\nbuild:\n  closed: true\n"
	openPigletYAML   = "name: pig-tern\n"
)

func wantClosedRefusal(t *testing.T, got, what string) {
	t.Helper()
	for _, want := range []string{"Piglet Binary (pig-tern)", "build.closed: true", what} {
		if !strings.Contains(got, want) {
			t.Fatalf("refusal %q lacks %q", got, want)
		}
	}
}

func TestClosedPigletBinaryRefusesExtensionFlag(t *testing.T) {
	closed := bakedPigletBinary(t, "pig-tern", closedPigletYAML)
	for _, source := range []string{"./x", "npm:@scope/pkg", "git:github.com/a/b", "builtin:mcp"} {
		wantClosedRefusal(t, pigletBinaryExtensionFlagRefusal(closed, []string{source}), "-e/--extension")
	}
	if got := pigletBinaryExtensionFlagRefusal(closed, nil); got != "" {
		t.Fatalf("no -e refused: %q", got)
	}
}

func TestOpenPigletBinaryAcceptsExtensionFlag(t *testing.T) {
	open := bakedPigletBinary(t, "pig-tern", openPigletYAML)
	if got := pigletBinaryExtensionFlagRefusal(open, []string{"./x", "npm:p", "git:g"}); got != "" {
		t.Fatalf("open Binary refused -e: %q", got)
	}
	if got := pigletBinaryExtensionFlagRefusal(pigletBinary{}, []string{"./x"}); got != "" {
		t.Fatalf("stock pig refused -e: %q", got)
	}
}

func TestClosedPigletBinaryRefusesSettingsExtensions(t *testing.T) {
	closed := bakedPigletBinary(t, "pig-tern", closedPigletYAML)
	wantClosedRefusal(t, pigletBinarySettingsExtensionsRefusal(closed, []string{"./x"}, nil), "settings.extensions")
	wantClosedRefusal(t, pigletBinarySettingsExtensionsRefusal(closed, nil, []string{"./x"}), "settings.extensions")
	if got := pigletBinarySettingsExtensionsRefusal(closed, nil, nil); got != "" {
		t.Fatalf("empty settings refused: %q", got)
	}
	open := bakedPigletBinary(t, "pig-tern", openPigletYAML)
	if got := pigletBinarySettingsExtensionsRefusal(open, []string{"./x"}, []string{"./y"}); got != "" {
		t.Fatalf("open Binary refused settings.extensions: %q", got)
	}
}

func TestClosedPigletBinaryRefusesPackageCommands(t *testing.T) {
	closed := bakedPigletBinary(t, "pig-tern", closedPigletYAML)
	for _, tc := range []struct {
		args []string
		what string
	}{
		{[]string{"install", "npm:p"}, "pig install"},
		{[]string{"install", "--validate-only", "./x"}, "pig install"},
		{[]string{"remove", "npm:p"}, "pig remove"},
		{[]string{"uninstall", "npm:p"}, "pig remove"},
		{[]string{"update", "--extensions"}, "pig update --extensions"},
		{[]string{"update", "--all"}, "pig update --extensions"},
		{[]string{"update", "--extension", "npm:p"}, "pig update --extensions"},
		{[]string{"update", "npm:p"}, "pig update --extensions"},
	} {
		opts, ok := parsePackageCommand(tc.args)
		if !ok {
			t.Fatalf("%v is not a package command", tc.args)
		}
		wantClosedRefusal(t, pigletBinaryPackageCommandRefusal(closed, opts), tc.what)
	}
	for _, args := range [][]string{{"update"}, {"update", "--self"}, {"update", "self"}, {"update", "--models"}, {"list"}} {
		opts, _ := parsePackageCommand(args)
		if got := pigletBinaryPackageCommandRefusal(closed, opts); got != "" {
			t.Fatalf("%v refused: %q", args, got)
		}
	}
}

func TestOpenPigletBinaryAcceptsPackageCommands(t *testing.T) {
	open := bakedPigletBinary(t, "pig-tern", openPigletYAML)
	for _, args := range [][]string{{"install", "npm:p"}, {"remove", "npm:p"}, {"update", "--extensions"}, {"update", "--all"}, {"update", "npm:p"}} {
		opts, _ := parsePackageCommand(args)
		if got := pigletBinaryPackageCommandRefusal(open, opts); got != "" {
			t.Fatalf("open Binary refused %v: %q", args, got)
		}
		if got := pigletBinaryPackageCommandRefusal(pigletBinary{}, opts); got != "" {
			t.Fatalf("stock pig refused %v: %q", args, got)
		}
	}
}

func TestClosedPigletBinaryPackageCommandExitsOneBeforeTouchingPackages(t *testing.T) {
	clearPigletEnv(t)
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	prev := currentPigletBinary
	currentPigletBinary = func() pigletBinary { return bakedPigletBinary(t, "pig-tern", closedPigletYAML) }
	t.Cleanup(func() { currentPigletBinary = prev })
	for _, args := range [][]string{{"install", "npm:p"}, {"remove", "npm:p"}, {"update", "--extensions"}} {
		var code int
		out := captureStderr(t, func() { code = runPackageCommand(args) })
		if code != 1 {
			t.Fatalf("%v exit = %d, want 1", args, code)
		}
		wantClosedRefusal(t, out, "build.closed")
	}
}

func TestClosedPigletBinaryRefusesMcpAdd(t *testing.T) {
	closed := bakedPigletBinary(t, "pig-tern", closedPigletYAML)
	wantClosedRefusal(t, pigletBinaryMcpRefusal(closed, []string{"mcp", "add", "srv", "--", "cmd"}), "pig mcp add")
	for _, args := range [][]string{{"mcp"}, {"mcp", "list"}, {"mcp", "remove", "srv"}, {"install", "add"}} {
		if got := pigletBinaryMcpRefusal(closed, args); got != "" {
			t.Fatalf("%v refused: %q", args, got)
		}
	}
	open := bakedPigletBinary(t, "pig-tern", openPigletYAML)
	if got := pigletBinaryMcpRefusal(open, []string{"mcp", "add", "srv"}); got != "" {
		t.Fatalf("open Binary refused mcp add: %q", got)
	}
}
