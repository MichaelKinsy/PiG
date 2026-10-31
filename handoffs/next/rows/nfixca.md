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
- cli/args.ts (help env line) | designed-out | n/a | n/a | PiG's --help has no env list; documented in docs/site environment-variables.md
- core/settings-schema.ts, schemas/settings.schema.json | ported | internal/codingagent/configschema/settings.go, internal/codingagent/settings.go (SelfUpdateCommand), schemas/settings.schema.json | test:configschema#TestCommittedSchemasMatchTheRenderedArtifacts, codingagent#TestSelfUpdateCommandSettingIsReadFromGlobalSettingsOnly | schema JSON is byte-identical to Pi's diff
- test/mcp-conformance/{baseline.json,README.md} | ported | test/mcp-conformance/ | n/a | client-registration dropped from basic-cimd passing
- docs cli.md/mcp.md/settings.md/environment-variables.md | ported | docs/site/docs/*, internal/pigdocs/content/{mcp,settings,environment-variables}.md | n/a | pigdocs has no cli.md; links dropped there

## D2/D39 notes
- 908aa7b7: Pi's `auto` registers when APP_NAME != "pi". PiG's app name is pig, so PiG's default behavior stays DCR; only `cimd` uses pi.dev's document. Ported faithfully (offersClientMetadataDocument); tests run the Pi cases with AppName "pi" and add the pig case. Owner call: if PiG should present as a pi.dev client by default it needs its own document (not done).
- 8bc94b19 / D39: configured command is a selection made before tier resolution; needs an update source (signed manifest) for the release; note printed as plain text (Pi renders markdown); no extension auto-upgrade after it (version of the replaced binary may differ); `PIG_SELF_UPDATE_COMMAND` alias.
- UnsupportedRemediation hint omits the literal `pig update` (existing non-looping test).

## (c) tests
- Renamed Go tests (integrator: update test-mapping/unit-evidence refs): TestMCPOAuthCIMDRegistersDynamicallyByDefaultEvenWhenTheServerSupportsDocuments -> ...RegistersDynamicallyWithDcrEvenWhenTheServerSupportsDocuments; TestMCPOAuthCIMDUsesPisDocumentWhenAuthorizationResponsesNameTheirIssuer -> ...UsesPisDocumentByDefaultWhenAuthorizationResponsesNameTheirIssuer.
- config.test.ts 3 cases, package-command-paths 4 cases (+1 PiG no-source case), mcp-command, mcp-extension, mcp-oauth-refresh (4 new cases), system-prompt.test.ts: ported.

## Pending / integrator
- Node shims (pi-dist system-prompt.js, skills.js, tools/edit.js, sdk-bundle chunks) are the vendored pin release with sha manifest; not hand-edited. They refresh with the pin bump (vendor-pi-dist.sh). Until then the oracle tests compare against old Pi and differ: TestSystemPromptSectionsMatchPi, TestValidateMcpServerConfigMatchesPi (`auto` message), TestMcpCommandMatchesPi (add --help), tool-definition oracles. Not red lane bugs.
- `make generate` (test/parity/normalization-inventory.json regenerates from the edited rpc scenarios 26/59/64: removed aliases "asks about pig itself", "pig packages", "pig topics", "Always read pig .md"; preamble alias now "running inside pig,").
- coding/testdata/pi_tool_definitions.json hand-updated to T text; re-dump when the pin moves.
