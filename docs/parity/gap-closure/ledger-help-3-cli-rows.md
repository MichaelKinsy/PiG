# lg-help-3: CLI row evidence map

All 65 `cli:` rows of `mapping-v1.0.4.json` are `pending`, and the detector has no CLI rule (`command-line row: no Go package symbol rule applies`). The table lists, for each row, the parity scenarios under `test/parity/scenarios` whose text contains the flag. A rule that closes a `cli:` row would need the flag registered in `cmd/pig` (parser case) and one asserting test or scenario that runs it. Rows with no scenario: `--local` (config, install, remove), `--all`, `--force`, `--self`, `--api-key`, `--append-system-prompt`, `--no-builtin-tools`, `--no-mcp`, `--theme`, `--use-theme`. All of them but config `--local` are asserted by `cmd/pig/cli_flag_rows_test.go` (Pi `cli/args.ts` and `package-manager-cli.ts`); `pig config --local` is run by `cmd/pig/config_command_test.go`.

| row | scenarios (first 3 of N) |
|---|---|
| `cli:pi-config/--approve` | cli-utils/11-install-missing-source.toml, cli-utils/12-install-unknown-option.toml, project-trust/08-global-extension-decides-project-trust.toml (8) |
| `cli:pi-config/--help` | cli-utils/12-install-unknown-option.toml (1) |
| `cli:pi-config/--local` |  (0) |
| `cli:pi-config/--no-approve` | cli-utils/11-package-uninstall-alias.toml, cli-utils/12-package-update-error.toml, cli-utils/11-install-missing-source.toml (9) |
| `cli:pi-install/--approve` | cli-utils/11-install-missing-source.toml, cli-utils/12-install-unknown-option.toml, project-trust/08-global-extension-decides-project-trust.toml (8) |
| `cli:pi-install/--help` | cli-utils/12-install-unknown-option.toml (1) |
| `cli:pi-install/--local` |  (0) |
| `cli:pi-install/--no-approve` | cli-utils/11-package-uninstall-alias.toml, cli-utils/12-package-update-error.toml, cli-utils/11-install-missing-source.toml (9) |
| `cli:pi-list/--approve` | cli-utils/11-install-missing-source.toml, cli-utils/12-install-unknown-option.toml, project-trust/08-global-extension-decides-project-trust.toml (8) |
| `cli:pi-list/--help` | cli-utils/12-install-unknown-option.toml (1) |
| `cli:pi-list/--no-approve` | cli-utils/11-package-uninstall-alias.toml, cli-utils/12-package-update-error.toml, cli-utils/11-install-missing-source.toml (9) |
| `cli:pi-remove/--approve` | cli-utils/11-install-missing-source.toml, cli-utils/12-install-unknown-option.toml, project-trust/08-global-extension-decides-project-trust.toml (8) |
| `cli:pi-remove/--help` | cli-utils/12-install-unknown-option.toml (1) |
| `cli:pi-remove/--local` |  (0) |
| `cli:pi-remove/--no-approve` | cli-utils/11-package-uninstall-alias.toml, cli-utils/12-package-update-error.toml, cli-utils/11-install-missing-source.toml (9) |
| `cli:pi-update/--all` |  (0) |
| `cli:pi-update/--approve` | cli-utils/11-install-missing-source.toml, cli-utils/12-install-unknown-option.toml, project-trust/08-global-extension-decides-project-trust.toml (8) |
| `cli:pi-update/--extension` | project-trust/15-update-ignores-untrusted-npm-command.toml, project-trust/18-approved-update-uses-project-npm-command.toml (2) |
| `cli:pi-update/--extensions` | project-trust/15-update-ignores-untrusted-npm-command.toml, project-trust/18-approved-update-uses-project-npm-command.toml (2) |
| `cli:pi-update/--force` |  (0) |
| `cli:pi-update/--help` | cli-utils/12-install-unknown-option.toml (1) |
| `cli:pi-update/--models` | model-resolver-selector/06-model-scope-toggle.toml, model-resolver-selector/23-model-picker-shift-tab-keeps-scope.toml, model-resolver-selector/18-model-picker-save-default-scope-order.toml (5) |
| `cli:pi-update/--no-approve` | cli-utils/11-package-uninstall-alias.toml, cli-utils/12-package-update-error.toml, cli-utils/11-install-missing-source.toml (9) |
| `cli:pi-update/--self` |  (0) |
| `cli:pi/--api-key` |  (0) |
| `cli:pi/--append-system-prompt` |  (0) |
| `cli:pi/--approve` | cli-utils/11-install-missing-source.toml, cli-utils/12-install-unknown-option.toml, project-trust/08-global-extension-decides-project-trust.toml (8) |
| `cli:pi/--continue` | session/03-session-continue-resume.toml (1) |
| `cli:pi/--exclude-tools` | rpc/40-rpc-exclude-tools.toml (1) |
| `cli:pi/--export` | export-html/03-export-cli-tools.toml, export-html/02-export-cli-ansi.toml, export-html/06-export-cli-skills.toml (5) |
| `cli:pi/--extension` | project-trust/15-update-ignores-untrusted-npm-command.toml, project-trust/18-approved-update-uses-project-npm-command.toml (2) |
| `cli:pi/--fork` | session/19-missing-session-id.toml, extensions-runtime/36-model-registry-session-manager.toml, extensions-runtime/54-extension-context-usage-presence.toml (3) |
| `cli:pi/--help` | cli-utils/12-install-unknown-option.toml (1) |
| `cli:pi/--list-models` | providers-registry/02-list-models-oauth-builtins.toml, providers-registry/05-list-models-extension-provider.toml, providers-registry/01-list-models-env-builtins.toml (17) |
| `cli:pi/--mode` | json/03-json-extension-no-ui.toml, json/08-json-tool-execution-update-partial-result.toml, json/07-json-failing-bash-result-iserror-content-hook.toml (186) |
| `cli:pi/--model` | json/03-json-extension-no-ui.toml, json/08-json-tool-execution-update-partial-result.toml, json/07-json-failing-bash-result-iserror-content-hook.toml (183) |
| `cli:pi/--models` | model-resolver-selector/06-model-scope-toggle.toml, model-resolver-selector/23-model-picker-shift-tab-keeps-scope.toml, model-resolver-selector/18-model-picker-save-default-scope-order.toml (5) |
| `cli:pi/--name` | cli-utils/12-cli-empty-session-name.toml, cli-utils/19-cli-name-selection-order.toml, cli-utils/13-cli-version-before-session-validation.toml (3) |
| `cli:pi/--no-approve` | cli-utils/11-package-uninstall-alias.toml, cli-utils/12-package-update-error.toml, cli-utils/11-install-missing-source.toml (9) |
| `cli:pi/--no-builtin-tools` |  (0) |
| `cli:pi/--no-context-files` | json/08-json-tool-execution-update-partial-result.toml, json/07-json-failing-bash-result-iserror-content-hook.toml, json/09-json-extension-object-member-order.toml (27) |
| `cli:pi/--no-extensions` | json/08-json-tool-execution-update-partial-result.toml, json/07-json-failing-bash-result-iserror-content-hook.toml, json/09-json-extension-object-member-order.toml (343) |
| `cli:pi/--no-mcp` |  (0) |
| `cli:pi/--no-prompt-templates` | selectors/12-extension-dialog-remapping.toml, selectors/13-extension-theme-results.toml, autocomplete/10-custom-editor-wrapper-state.toml (10) |
| `cli:pi/--no-session` | json/03-json-extension-no-ui.toml, json/02-json-mode-extension-command.toml, json/04-json-real-provider-error.toml (60) |
| `cli:pi/--no-skills` | json/08-json-tool-execution-update-partial-result.toml, json/07-json-failing-bash-result-iserror-content-hook.toml, json/09-json-extension-object-member-order.toml (41) |
| `cli:pi/--no-themes` | project-trust/10-startup-trust-prompt-wording.toml, project-trust/09-cancel-trust-shows-warning-with-extensions-off.toml, project-trust/14-resumed-denied-project-warning-follows-history.toml (4) |
| `cli:pi/--no-tools` | rpc/41-rpc-tools-allowlist-overrides-no-tools.toml (1) |
| `cli:pi/--offline` | json/04-json-real-provider-error.toml, model-runtime-store-catalog/20-model-command-refreshed-match.toml, model-runtime-store-catalog/21-unavailable-scoped-models.toml (90) |
| `cli:pi/--print` | json/02-json-mode-extension-command.toml, print/05-print-extension-session-id.toml, print/03-print-extension-no-ui.toml (41) |
| `cli:pi/--prompt-template` | slash-commands/09-prompt-template-unicode-whitespace.toml, slash-commands/10-prompt-template-yaml-diagnostic.toml, slash-commands/11-prompt-template-yaml-location.toml (4) |
| `cli:pi/--provider` | cli-utils/22-cli-provider-requires-model.toml, footer/testdata/branch-watch/launch.py (2) |
| `cli:pi/--resume` | cli-utils/03-startup-resume-picker.toml, cli-utils/10-startup-resume-picker-cancel.toml, session/21-session-search-resume-picker.toml (4) |
| `cli:pi/--session` | json/08-json-tool-execution-update-partial-result.toml, json/07-json-failing-bash-result-iserror-content-hook.toml, json/09-json-extension-object-member-order.toml (266) |
| `cli:pi/--session-dir` | json/08-json-tool-execution-update-partial-result.toml, json/07-json-failing-bash-result-iserror-content-hook.toml, json/09-json-extension-object-member-order.toml (254) |
| `cli:pi/--session-id` | cli-utils/13-cli-version-before-session-validation.toml (1) |
| `cli:pi/--skill` | slash-commands/07-skill-command-system-prompt.toml, autocomplete/08-extension-command-before-skill.toml, project-trust/10-untrusted-ancestor-skills-explicit-only.toml (6) |
| `cli:pi/--system-prompt` | json/08-json-tool-execution-update-partial-result.toml, json/07-json-failing-bash-result-iserror-content-hook.toml, json/09-json-extension-object-member-order.toml (27) |
| `cli:pi/--theme` |  (0) |
| `cli:pi/--thinking` | rpc/34-rpc-initial-thinking.toml (1) |
| `cli:pi/--tools` | tools/05-print-find-tool-exec.toml, tools/15-print-find-scoped-ignores.toml, rpc/41-rpc-tools-allowlist-overrides-no-tools.toml (3) |
| `cli:pi/--tui-mode` | slash-commands/13-armin-bitmap.toml, slash-commands/11-changelog-complete-history.toml, fullscreen/04-main-screen-assistant-streaming.toml (11) |
| `cli:pi/--use-theme` |  (0) |
| `cli:pi/--verbose` | startup/04-startup-verbose-collapse.toml, project-trust/07-resume-picker-selects-cwd-before-resources.toml, project-trust/06-session-cwd-precedes-project-resource-loading.toml (3) |
| `cli:pi/--version` | cli-utils/13-cli-version-before-session-validation.toml, cli-utils/testdata/package-npm-name/project/npm, cli-utils/15-package-npm-name-lexical.toml (6) |
