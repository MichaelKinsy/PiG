package configvalue

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Pi's template parsing in resolve-config-value.ts (getConfigValueEnvVarName(s), getMissingConfigValueEnvVarNames, isConfigValueConfigured, resolveConfigValueUncached for non-command values) runs against the vendored Pi module for the same config strings and provider-scoped env.
func TestConfigValueTemplatesMatchPiOracle(t *testing.T) {
	configs := []string{
		"", "plain", "$", "$$", "$!", "$$$", "$$A", "$!A", "$A", "$A_B", "$A-B", "$1A", "$_a1", "${A}", "${ A }", "${}", "${A", "${A}}", "${1}", "${A-B}", "${A}${B}",
		"x$A", "$Ay", "$A$B", "$A.$B", "a$$b$!c$", "${A", "$ ${A}", "${A}$", "pre-${A}-post", "$A\n", "${A\n}", "$é", "$Aé", "${é}", "Bearer $TOKEN", "Bearer ${TOKEN}x",
		"$EMPTY", "${EMPTY}x", "$UNSET", "$A$UNSET", "!echo hi", "!", "!$A", "\\$A", "$$$A", "$!$A", "${A}{B}", "$A}", "${$A}", "${A$B}",
	}
	env := map[string]string{"A": "alpha", "B": "beta", "A_B": "ab", "_a1": "u", "TOKEN": "tok", "EMPTY": "", "é": "accent", "1": "one"}
	type input struct {
		Configs []string          `json:"configs"`
		Env     map[string]string `json:"env"`
	}
	type result struct {
		Name       string   `json:"name"`
		Names      []string `json:"names"`
		Missing    []string `json:"missing"`
		Configured bool     `json:"configured"`
		Command    bool     `json:"command"`
		Resolved   *string  `json:"resolved"`
	}
	var want []result
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/resolve-config-value.js");
emit(input.configs.map((c) => ({
	name: mod.getConfigValueEnvVarName(c) ?? "",
	names: mod.getConfigValueEnvVarNames(c),
	missing: mod.getMissingConfigValueEnvVarNames(c, input.env),
	configured: mod.isConfigValueConfigured(c, input.env),
	command: mod.isCommandConfigValue(c),
	resolved: mod.isCommandConfigValue(c) ? null : (mod.resolveConfigValueUncached(c, input.env) ?? null),
})));`, input{configs, env}, &want)
	for i, config := range configs {
		got := result{
			Name: GetConfigValueEnvVarName(config), Names: GetConfigValueEnvVarNames(config), Missing: GetMissingConfigValueEnvVarNames(config, env),
			Configured: IsConfigValueConfigured(config, env), Command: IsCommandConfigValue(config),
		}
		if !got.Command {
			if resolved, ok := resolveTemplate(parseConfigValueTemplate(config), env); ok {
				got.Resolved = &resolved
			}
		}
		if got.Names == nil {
			got.Names = []string{}
		}
		if got.Missing == nil {
			got.Missing = []string{}
		}
		if !reflect.DeepEqual(got, want[i]) {
			t.Errorf("%q:\n Go %+v\n Pi %+v", config, got, want[i])
		}
	}
}
