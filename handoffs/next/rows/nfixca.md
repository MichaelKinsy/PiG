# N-FIX-CA rows (branch fix/follow-nfixca, base 65411f70dd, Pi target 6d4b7443)

Commits: 705d047c = red 3503e06493, green b4467c9492; 8bc94b19 = red 4e02d3df83, green 3bd831d554; 908aa7b7 = green c8288ae82a (tests ported in the same commit); fixups + docs + rows in the last commit.

## (a) upstream files
path | disposition | PiG files | evidence | rationale
- core/system-prompt.ts | ported | internal/codingagent/prompts/{coding,docs_section}.go | test:internal/codingagent/prompts#TestUpstreamSystemPromptSecondHalfExactSections,#TestToolsWithoutSnippetsAreNotListed | preamble, tools tail, docs section (D2 product names kept: "PiG documentation", "pig" preamble)
- core/skills.ts | ported | internal/codingagent/prompts/coding.go, extensions/sdk/session_skill_helpers.go (+ .go.gz regenerated), sdk-rs/py/lisp copies | test:prompts#TestFormatSkillsForPrompt* (upstream_skills_test.go), sdk-lisp tests | skills hint sentence
- core/tools/edit.ts | ported | internal/codingagent/tools/edit.go, coding/testdata/pi_tool_definitions.json | test:internal/codingagent/tools#TestPromptContributions*, coding#TestSessionPromptOptions* | two guidelines
- core/mcp-servers.ts | ported | coding/extension/mcp_servers.go (validate, IsClientMetadataDocumentCallback) | test:coding/mcpext#TestMCPConfigValidatesTheOAuthCallbackURLScopeAndClientName | `auto`
- extensions/mcp/oauth.ts | ported | coding/mcpext/oauth.go | test:coding/mcpext#TestMCPOAuthCIMD* | see D2 note below
- extensions/mcp/cli.ts | ported | coding/mcpext/cli.go, testdata/mcp_help.txt | test:coding/mcpext#TestMcpCommandAdd... (cli_upstream_test.go) | --oauth-client-registration
- config.ts (getConfiguredSelfUpdateArgv, makeConfiguredSelfUpdateCommand, unavailable-hint) | ported | internal/codingagent/selfupdate_configured.go, selfupdate_tier.go (UnsupportedRemediation) | test:internal/codingagent#TestConfigConfiguredSelfUpdateCommand* | PIG_ alias first
- package-manager-cli.ts | ported | coding/cli/self_update.go, self_update_configured.go | test:coding/cli#TestConfiguredSelfUpdate* | D39 tier: explicit configured owner, no fallthrough
- cli/args.ts (help env line) | generated at the pin bump | coding/cli/help_upstream.txt via gen-help.sh | test:coding/cli#TestHelpMatchesPi | Pi's env list is mirrored verbatim; the PI_SELF_UPDATE_COMMAND row arrives with the help regeneration at the pin bump; docs/site environment-variables.md documents it now
- core/settings-schema.ts, schemas/settings.schema.json | ported | internal/codingagent/configschema/settings.go, internal/codingagent/settings.go (SelfUpdateCommand), schemas/settings.schema.json | test:configschema#TestCommittedSchemasMatchTheRenderedArtifacts, codingagent#TestSelfUpdateCommandSettingIsReadFromGlobalSettingsOnly | schema JSON is byte-identical to Pi's diff
- test/mcp-conformance/{baseline.json,README.md} | ported | test/mcp-conformance/ | n/a | client-registration dropped from basic-cimd passing
- docs cli.md/mcp.md/settings.md/environment-variables.md | ported | docs/site/docs/*, internal/pigdocs/content/{mcp,settings,environment-variables}.md | n/a | pigdocs has no cli.md; links dropped there

## D2/D39 notes
- 908aa7b7: Pi's `auto` registers when APP_NAME != "pi". PiG's app name is pig, so PiG's default behavior stays DCR; only `cimd` uses pi.dev's document. Ported faithfully (offersClientMetadataDocument); tests run the Pi cases with AppName "pi" and add the pig case. Owner call: if PiG should present as a pi.dev client by default it needs its own document (not done).
- 8bc94b19 / D39: configured command is a selection made before tier resolution; needs an update source (signed manifest) for the release; note printed as plain text (Pi renders markdown); no extension auto-upgrade after it (version of the replaced binary may differ); `PIG_SELF_UPDATE_COMMAND` alias.
- UnsupportedRemediation hint omits the literal `pig update` (existing non-looping test).

## (c) tests
- No Go test was renamed. TestMCPOAuthCIMDRegistersDynamicallyByDefaultEvenWhenTheServerSupportsDocuments now runs as the application `pig` (Pi's `auto` rule), the Pi default-document case keeps TestMCPOAuthCIMDUsesPisDocumentWhenAuthorizationResponsesNameTheirIssuer with AppName `pi` and default settings, and the new dcr case is TestMCPOAuthCIMDRegistersDynamicallyWithDcrEvenWhenTheServerSupportsDocuments.
- config.test.ts 3 cases, package-command-paths 4 cases (+1 PiG no-source case), mcp-command, mcp-extension, mcp-oauth-refresh (4 new cases), system-prompt.test.ts: ported.

## Integrator: Node shims and sdk-bundle refresh (do at the pin bump)
Refresh the vendored pin release with automation/gen/vendor-pi-dist.sh against Pi 6d4b7443's installed packages in the same step as the pin bump, then regenerate vendor-manifest.json. This lane changes Pi text that lives in these shim files, so they must be refreshed, not hand-edited:
- shims/pi-dist/pi-coding-agent/core/system-prompt.js, core/skills.js, core/tools/edit.js (705d047c);
- shims/pi-dist/pi-coding-agent/sdk-bundle/chunk-HXXU3UR4.js (system prompt, skills) and chunk-YO7W7VLN.js (edit guidelines);
- the MCP bundle chunks carrying extensions/mcp/{oauth,cli}.js and core/mcp-servers.js (908aa7b7), config.js and package-manager-cli.js (8bc94b19).
After that refresh these oracle tests turn green: TestSystemPromptSectionsMatchPi, TestValidateMcpServerConfigMatchesPi, TestMcpCommandMatchesPi, the tool-definition oracles, and the parity scenarios cli-utils 33-37 (they compare against Pi 6d4b7443 and cannot pass against the pinned e7a0df27 release, which lacks the flag, `auto` and the config.ts functions). Re-dump coding/testdata/pi_tool_definitions.json then too.

## Parity scenarios (new)
- cli-utils/33..36 `pig mcp add --oauth-client-registration` (valid dcr, invalid mode, cimd with --oauth-client-name, stdio-only). Probed against Pi 6d4b7443 built on imladris; pig output matches byte for byte after the D2 `pig`->`pi` and agent-dir normalizations.
- cli-utils/37 configured self-update command (Pi probe testdata/configured-self-update-pi.mjs vs Go TestConfiguredSelfUpdateParityProbe).
- The self-update behavior against a release manifest has no Pi comparator (Pi fetches pi.dev; PiG the signed manifest, D39): covered by coding/cli TestConfiguredSelfUpdate*.

## Pending / integrator
- Node shims (pi-dist system-prompt.js, skills.js, tools/edit.js, sdk-bundle chunks) are the vendored pin release with sha manifest; not hand-edited. They refresh with the pin bump (vendor-pi-dist.sh). Until then the oracle tests compare against old Pi and differ: TestSystemPromptSectionsMatchPi, TestValidateMcpServerConfigMatchesPi (`auto` message), TestMcpCommandMatchesPi (add --help), tool-definition oracles. Not red lane bugs.
- `make generate` (test/parity/normalization-inventory.json regenerates from the edited rpc scenarios 26/59/64: removed aliases "asks about pig itself", "pig packages", "pig topics", "Always read pig .md"; preamble alias now "running inside pig,").
- coding/testdata/pi_tool_definitions.json hand-updated to T text; re-dump when the pin moves.

## Generated files (this lane)
- Committed: test/parity/normalization-inventory.json, test/parity/coverage.md, AGENTS.md (coverage table) from `make normalization-inventory` and `make coverage RESULTS=`; `coverage-drift` and `normalization-inventory-check` exit 0 on this branch.
- Not committed (integrator/merger-owned): docs/parity/DIVERGENCES.md (D39 record), test/parity/interfaces/pig-go.json (new Go interface IDs: SelfUpdateRelease, ConfiguredSelfUpdateArgv, MakeConfiguredSelfUpdateCommand, RunConfiguredSelfUpdateCommand, IsClientMetadataDocumentCallback).
- `make generate` still stops at `known-gaps` ("upstream test release gate blocked", findings in packages/chord and packages/durable test files, other lanes' rows); nothing from this lane is in the findings.
- Pin-gated failures until the pin bump: configschema TestRenderedSchemasAreByteEqualToThePinnedPiPackage / ...UpstreamMirror (settings.schema.json now has selfUpdateCommand, byte-equal to Pi 6d4b7443), mcpext TestLoadMcpConfigMatchesPiOnRandomFiles (3 of 1500, the `auto` value).
