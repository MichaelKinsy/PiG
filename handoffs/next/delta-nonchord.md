# Non-chord pending delta at Pi c23b9ec1 (from v1.5.0): owner per row

Source: `go run ./test/parity/cmd/upstreamdelta` on follow/pi-main 6f2e1200a (174 non-chord pending, plus chord not covered here), `test-mapping-v1.5.0+c23b9ec1.json` (152 non-chord pending/partial test rows).

## Finding
The Go code and Pi-named Go tests for this delta are on follow/pi-main already (Pi main commits, D24x/D25x divergences cited in test comments): token check of every added Pi line against pi-main Go finds only TypeScript-local names missing. Every Pi test file but `subagent-extension.test.ts` (an example extension, not a pending source row) and `indexeddb-storage.test.ts` (browser-only; designed-out) has a Go file naming it. The open work is ROW FLIPS (upstream-sync disposition + test-mapping) with evidence, not new code. Exceptions that exist only on a lane branch (not pi-main): durable per-conversation compaction, SqliteStorage batch commit, Tx.Submission, durable stream options (follow/durable); mcp package files (follow/mcp); edit-diff window / grep #11807 / image-resize #10695 / diff indentation (follow/coding-b).

## Totals
| owner | src rows | test rows |
|---|---|---|
| ai (flipped in branch) | 21 | 31 |
| ai,small (flipped in branch) | 0 | 1 |
| coding-a | 12 | 0 |
| coding-a,mcp (flipped in branch) | 19 | 12 |
| coding-b | 4 | 0 |
| coding-b (flipped in branch) | 10 | 13 |
| delta-nonchord | 48 | 50 |
| durable | 59 | 39 |
| durable (flipped in branch) | 0 | 5 |
| mermaid (flipped in branch) | 1 | 1 |

## Source rows (`test/parity/upstream-sync/v1.5.0+c23b9ec1.toml`)
| file | owner | Go target on pi-main |
|---|---|---|
| agent/src/proxy.ts | delta-nonchord |  |
| ai/src/api/anthropic-messages.ts | ai (flipped in branch) | ai/anthropic.go, ai/anthropic_client.go, ai/anthropic_federation.go |
| ai/src/api/azure-openai-responses.ts | ai (flipped in branch) | ai/azure_openai_responses.go, ai/direct_simple.go |
| ai/src/api/bedrock-converse-stream.lazy.ts | ai (flipped in branch) |  |
| ai/src/api/lazy.ts | coding-b | ai/lazy_api_upstream_test.go, ai/models_runtime_stream.go, ai/provider_streams.go |
| ai/src/api/openai-codex-responses.ts | ai (flipped in branch) | ai/direct_simple.go, ai/openai_codex_frames.go, ai/openai_codex_http.go |
| ai/src/api/openai-completions.ts | ai (flipped in branch) | ai/direct_simple.go, ai/openai.go, ai/openai_error.go |
| ai/src/api/openai-responses.ts | ai (flipped in branch) | ai/direct_simple.go, ai/openai_error.go, ai/openai_responses.go |
| ai/src/api/simple-options.ts | ai (flipped in branch) | ai/simple_options_answer_room_test.go |
| ai/src/auth/oauth/load.ts | ai (flipped in branch) |  |
| ai/src/env-api-keys.ts | ai (flipped in branch) |  |
| ai/src/lazy-bedrock-provider.ts | ai (flipped in branch) |  |
| ai/src/lazy-oauth.ts | ai (flipped in branch) |  |
| ai/src/models.generated.ts | ai (flipped in branch) |  |
| ai/src/models.ts | coding-b | ai/models_catalog_codec.go, ai/models_options.go, ai/models_runtime.go |
| ai/src/providers/all.ts | ai (flipped in branch) | ai/builtin_providers.go |
| ai/src/providers/compat-schema.ts | ai (flipped in branch) |  |
| ai/src/providers/github-copilot.ts | ai (flipped in branch) | ai/github_copilot_filter.go |
| ai/src/providers/openai-codex.ts | ai (flipped in branch) |  |
| ai/src/providers/qwen-token-plan-individual-cn.models.ts | ai (flipped in branch) |  |
| ai/src/providers/qwen-token-plan-individual-cn.ts | ai (flipped in branch) |  |
| ai/src/types.ts | ai (flipped in branch) | ai/deferred_option.go, ai/known_provider.go, ai/model_compat_json.go |
| ai/src/utils/error-body.ts | ai (flipped in branch) | ai/errors.go, internal/jsonstringify/json.go |
| ai/src/utils/retry.ts | ai (flipped in branch) | durable/core/turn/patterns.go |
| codemode/src/declarations.ts | delta-nonchord | codemode/declarations.go |
| codemode/src/index.ts | delta-nonchord |  |
| coding-agent/src/cli/args.ts | delta-nonchord |  |
| coding-agent/src/cli/auth-command.ts | coding-a |  |
| coding-agent/src/cli/auth-login.ts | coding-a | coding/cli/auth_login.go |
| coding-agent/src/cli/auth-logout.ts | coding-a | coding/cli/auth_logout.go |
| coding-agent/src/cli/auth-terminal.ts | coding-a | coding/cli/auth_terminal.go |
| coding-agent/src/cli/config-selector.ts | delta-nonchord |  |
| coding-agent/src/core/agent-session-runtime.ts | coding-b | coding/runtime_replacement.go, coding/session.go, coding/session_extension_replacement.go |
| coding-agent/src/core/agent-session.ts | coding-b | coding/extension/tool_filter.go, coding/session.go, coding/session_bind.go |
| coding-agent/src/core/auth-storage.ts | delta-nonchord | ai/auth.go, ai/auth_mutex.go, ai/auth_reload.go |
| coding-agent/src/core/bash-executor.ts | delta-nonchord |  |
| coding-agent/src/core/cache-warmer.ts | delta-nonchord | coding/extension/cache_warming.go |
| coding-agent/src/core/compaction/utils.ts | delta-nonchord |  |
| coding-agent/src/core/extensions/index.ts | delta-nonchord |  |
| coding-agent/src/core/extensions/loader.ts | delta-nonchord | coding/discover_extensions.go, coding/extension/factoryload/load.go, coding/extension/host/subprocess/builder_node.go |
| coding-agent/src/core/extensions/runner.ts | delta-nonchord | coding/extension/host/inproc/cache_warming.go, coding/extension/host/inproc/mcp_servers.go, coding/extension/host/inproc/runner.go |
| coding-agent/src/core/extensions/types.ts | delta-nonchord | coding/define_tool.go, coding/extension/custom.go, coding/extension/extension_event.go |
| coding-agent/src/core/keybindings.ts | delta-nonchord |  |
| coding-agent/src/core/mcp-servers.ts | coding-a | coding/extension/mcp_servers.go, coding/extension/tool_filter.go, coding/extension/tool_filter_test.go |
| coding-agent/src/core/model-resolver.ts | delta-nonchord | coding/cli/model.go, coding/cli/resolve_cli_model.go, coding/cli/startup_model.go |
| coding-agent/src/core/model-runtime.ts | delta-nonchord | coding/extension/host/subprocess/native_provider.go, coding/model_availability.go, coding/model_catalog_backend.go |
| coding-agent/src/core/package-manager.ts | coding-b (flipped in branch) | coding/cli/builtin_extension_session_test.go, coding/cli/configured_resources.go, coding/cli/package_progress.go |
| coding-agent/src/core/prompt-templates.ts | coding-b (flipped in branch) | internal/codingagent/prompt_diagnostics.go |
| coding-agent/src/core/provider-composer.ts | delta-nonchord | coding/model_stream_forwarding.go, coding/registered_stream_provider.go, internal/codingagent/model_configured_auth_status.go |
| coding-agent/src/core/provider-matching.ts | delta-nonchord | internal/codingagent/provider_matching.go |
| coding-agent/src/core/radius-mcp.ts | coding-a | internal/codingagent/radius_mcp.go |
| coding-agent/src/core/runtime-credentials.ts | delta-nonchord |  |
| coding-agent/src/core/tools/bash.ts | delta-nonchord | internal/codingagent/tools/bash.go |
| coding-agent/src/core/tools/edit-diff.ts | coding-b (flipped in branch) | internal/codingagent/tools/edit_diff.go |
| coding-agent/src/core/tools/edit.ts | coding-b (flipped in branch) | internal/codingagent/tools/edit.go |
| coding-agent/src/core/tools/find.ts | delta-nonchord | internal/codingagent/tools/find.go |
| coding-agent/src/core/tools/grep.ts | coding-b (flipped in branch) | internal/codingagent/tools/grep.go |
| coding-agent/src/core/tools/ls.ts | delta-nonchord | internal/codingagent/tools/ls.go, internal/codingagent/tools/ls_create_tool_test.go |
| coding-agent/src/core/tools/read.ts | coding-b (flipped in branch) | internal/codingagent/tools/context_cwd.go, internal/codingagent/tools/read.go |
| coding-agent/src/core/tools/truncate.ts | coding-b (flipped in branch) | internal/truncate/tail.go |
| coding-agent/src/extensions/codemode/execute.ts | delta-nonchord | coding/extension/builtin/codemode/execute.go, coding/extension/builtin/codemode/models.go, coding/extension/builtin/codemode/run.go |
| coding-agent/src/extensions/codemode/tool.ts | delta-nonchord | coding/extension/builtin/codemode/description.go, coding/extension/builtin/codemode/description_text.go, coding/extension/builtin/codemode/tool.go |
| coding-agent/src/extensions/llama/client.ts | delta-nonchord | internal/codingagent/llama/client.go |
| coding-agent/src/extensions/llama/ui.ts | coding-a | internal/codingagent/llama/ui.go |
| coding-agent/src/extensions/mcp/cli.ts | coding-a | coding/mcpext/cli.go |
| coding-agent/src/extensions/mcp/config.ts | delta-nonchord | coding/mcpext/config.go |
| coding-agent/src/extensions/mcp/index.ts | delta-nonchord | coding/mcpext/extension.go |
| coding-agent/src/extensions/mcp/oauth.ts | delta-nonchord | coding/mcpext/oauth.go |
| coding-agent/src/extensions/mcp/resources.ts | delta-nonchord | coding/mcpext/resources.go |
| coding-agent/src/extensions/mcp/runtime.ts | delta-nonchord | coding/mcpext/runtime.go |
| coding-agent/src/extensions/mcp/tools.ts | delta-nonchord | coding/mcpext/tools.go |
| coding-agent/src/extensions/tool-search/tool.ts | coding-a | coding/extension/builtin/toolsearch/tool.go, coding/extension/builtin/toolsearch/toolsearch.go |
| coding-agent/src/index.ts | delta-nonchord |  |
| coding-agent/src/main.ts | coding-a | coding/cli/auth_run.go, coding/cli/cli_runtime_build.go, coding/cli/cli_session_factory.go |
| coding-agent/src/modes/interactive/components/branch-summary-message.ts | delta-nonchord |  |
| coding-agent/src/modes/interactive/components/compaction-summary-message.ts | delta-nonchord |  |
| coding-agent/src/modes/interactive/components/custom-message.ts | delta-nonchord |  |
| coding-agent/src/modes/interactive/components/diff.ts | coding-b (flipped in branch) | tui/diff_component.go |
| coding-agent/src/modes/interactive/components/extension-selector.ts | delta-nonchord | internal/codingagent/ext_ui_context.go, tui/extension_selector.go |
| coding-agent/src/modes/interactive/components/login-dialog.ts | delta-nonchord | tui/login_dialog.go |
| coding-agent/src/modes/interactive/components/mermaid.ts | mermaid (flipped in branch) | internal/codingagent/mermaid_definition_parser.go, internal/codingagent/mermaid_fence_parser.go, internal/codingagent/mermaid_heading_parser.go |
| coding-agent/src/modes/interactive/components/model-selector.ts | delta-nonchord | tui/model_selector_runtime.go |
| coding-agent/src/modes/interactive/components/skill-invocation-message.ts | delta-nonchord |  |
| coding-agent/src/modes/interactive/components/tree-selector.ts | delta-nonchord |  |
| coding-agent/src/modes/interactive/interactive-mode.ts | coding-a | coding/extension/autocomplete.go, internal/codingagent/auth_warnings.go, internal/codingagent/autocomplete_source_tag.go |
| coding-agent/src/modes/interactive/theme/theme.ts | delta-nonchord | internal/codingagent/extension_diagnostics.go, tui/component_themes.go, tui/highlight.go |
| coding-agent/src/modes/print-mode.ts | delta-nonchord |  |
| coding-agent/src/modes/rpc/rpc-mode.ts | delta-nonchord | coding/cli/rpc_admission.go, coding/cli/rpc_dispatch.go, coding/cli/rpc_js_fields.go |
| coding-agent/src/package-manager-cli.ts | coding-b (flipped in branch) | coding/cli/package_command_trust.go, coding/cli/package_model_catalogs.go |
| coding-agent/src/utils/changelog.ts | delta-nonchord | internal/codingagent/changelog_links.go |
| coding-agent/src/utils/image-resize.ts | coding-b (flipped in branch) |  |
| coding-agent/src/utils/terminal-question.ts | coding-a | internal/terminalquestion/terminalquestion.go |
| coding-agent/src/utils/web-url.ts | delta-nonchord | internal/weburl/weburl.go |
| durable/src/codemode/codemode.ts | durable | durable/codemode/call.go, durable/codemode/docs.go, durable/codemode/tool.go |
| durable/src/codemode/models.ts | durable | durable/codemode/models.go |
| durable/src/documents.ts | durable | durable/documents.go |
| durable/src/entries.ts | durable | durable/entries.go |
| durable/src/env/index.ts | durable | durable/env/env.go |
| durable/src/env/node.ts | durable | durable/env/node/binary_reader.go, durable/env/node/dir_reader_oracle_test.go, durable/env/node/exec.go |
| durable/src/env/node-watch.ts | durable | durable/env/node/watch.go |
| durable/src/harness/agent.ts | durable | durable/harness/agent.go, durable/harness/agent_defaults_test.go |
| durable/src/harness/compaction.ts | durable | durable/harness/compaction.go |
| durable/src/harness/context.ts | durable | durable/harness/context.go |
| durable/src/harness/define.ts | durable | durable/harness/agent_defaults_test.go, durable/harness/define.go |
| durable/src/harness/generation.ts | durable | durable/harness/generation.go |
| durable/src/harness/harness.ts | durable | durable/core/driver/observe.go, durable/harness/harness.go |
| durable/src/harness/inbox.ts | durable | durable/harness/inbox.go |
| durable/src/harness/json.ts | durable | durable/harness/json.go |
| durable/src/harness/live.ts | durable | durable/harness/live.go |
| durable/src/harness/partial-changes.ts | durable | durable/harness/partial_changes.go |
| durable/src/harness/prompt.ts | durable | durable/harness/prompt.go |
| durable/src/harness/provider.ts | durable | durable/harness/provider.go |
| durable/src/harness/registry-reader.ts | durable | durable/harness/registry_reader.go |
| durable/src/harness/scheduler.ts | durable | durable/core/session/index_sizes.go, durable/core/session/sched_index.go, durable/core/session/telemetry.go |
| durable/src/harness/submissions.ts | durable | durable/harness/submissions.go |
| durable/src/harness/task-graph.ts | durable | durable/harness/task_graph.go |
| durable/src/harness/tool.ts | durable | durable/harness/structured_output.go, durable/harness/tool.go |
| durable/src/harness/transcript.ts | durable | durable/harness/transcript.go |
| durable/src/harness/types.ts | durable | durable/harness/types.go, durable/harness_types_test.go |
| durable/src/harness/usage.ts | durable | durable/harness/usage.go |
| durable/src/harness/util.ts | durable | durable/harness/util.go |
| durable/src/harness/view.ts | durable | durable/harness/view.go |
| durable/src/index.ts | durable | durable/doc.go |
| durable/src/session/cached-storage.ts | durable | durable/session/cached_storage.go |
| durable/src/session/forks.ts | durable | durable/session/forks.go |
| durable/src/session/keys.ts | durable | durable/session/keys.go |
| durable/src/session/session.ts | durable | durable/core/session/retention.go, durable/core/sqlhost/host.go, durable/session/guard.go |
| durable/src/session/transaction.ts | durable | durable/session/transaction.go |
| durable/src/session/writer.ts | durable | durable/session/writer.go |
| durable/src/storage/indexeddb.ts | durable |  |
| durable/src/storage/jsonl/node.ts | durable | durable/storage/jsonl/node/node.go |
| durable/src/storage/jsonl/storage.ts | durable | durable/storage/jsonl/poisoned_error_test.go, durable/storage/jsonl/storage.go |
| durable/src/storage/memory.ts | durable | durable/storage/memory.go |
| durable/src/storage/sqlite/commit.ts | durable |  |
| durable/src/storage/sqlite/rows.ts | durable |  |
| durable/src/storage/sqlite/statements.ts | durable |  |
| durable/src/storage/sqlite/storage.ts | durable | durable/storage/sqlite/storage.go |
| durable/src/storage/store/commit.ts | durable | durable/storage/store/commit.go |
| durable/src/storage/store/documents.ts | durable | durable/storage/store/documents.go |
| durable/src/storage/store/queries.ts | durable | durable/storage/store/queries.go |
| durable/src/storage/store/storage.ts | durable | durable/storage/store/storage.go |
| durable/src/storage/store/tables.ts | durable | durable/storage/store/tables.go |
| durable/src/testing/commit-writes.ts | durable | durable/durabletest/commit_writes.go |
| durable/src/testing/env-conformance.ts | durable | durable/durabletest/env_conformance.go |
| durable/src/testing/storage-benchmark.ts | durable | durable/durabletest/storage_benchmark.go, durable/storage/storage_write_benchmark_test.go |
| durable/src/testing/storage-conformance.ts | durable | durable/durabletest/storage_conformance.go |
| durable/src/tools/bash.ts | durable | durable/tools/bash.go |
| durable/src/tool-search/tool-search.ts | durable | durable/toolsearch/toolsearch.go |
| durable/src/tools/file-mutation-queue.ts | durable | durable/tools/file_mutation_queue.go |
| durable/src/tools/path-utils.ts | durable | durable/tools/path_utils.go |
| durable/src/tools/read.ts | durable | durable/tools/read.go, durable/tools/read_image.go |
| durable/src/types.ts | durable | durable/types.go |
| mcp/src/config.ts | coding-a,mcp (flipped in branch) | mcp/config.go |
| mcp/src/index.ts | coding-a,mcp (flipped in branch) |  |
| mcp/src/input-schema.ts | coding-a,mcp (flipped in branch) | mcp/input_schema.go |
| mcp/src/naming.ts | coding-a,mcp (flipped in branch) | mcp/naming.go |
| mcp/src/oauth/auth-provider.ts | coding-a,mcp (flipped in branch) | mcp/oauth/auth_provider.go |
| mcp/src/oauth/callback.ts | coding-a,mcp (flipped in branch) | mcp/oauth/callback.go |
| mcp/src/oauth/client-settings.ts | coding-a,mcp (flipped in branch) | mcp/oauth/client_settings.go |
| mcp/src/oauth/discovery.ts | coding-a,mcp (flipped in branch) | mcp/oauth/discovery.go |
| mcp/src/oauth/errors.ts | coding-a,mcp (flipped in branch) | mcp/oauth/errors.go, mcp/oauth/errors_class_test.go |
| mcp/src/oauth/flow.ts | coding-a,mcp (flipped in branch) | mcp/oauth/flow.go |
| mcp/src/oauth/index.ts | coding-a,mcp (flipped in branch) |  |
| mcp/src/oauth/loopback.ts | coding-a,mcp (flipped in branch) | mcp/oauth/loopback.go |
| mcp/src/oauth/sign-in.ts | coding-a,mcp (flipped in branch) | mcp/oauth/sign_in.go |
| mcp/src/oauth/types.ts | coding-a,mcp (flipped in branch) | mcp/oauth/jsonextra.go, mcp/oauth/types.go |
| mcp/src/output-limit.ts | coding-a,mcp (flipped in branch) | mcp/output_limit.go |
| mcp/src/transports/lines.ts | coding-a,mcp (flipped in branch) | mcp/lines.go |
| mcp/src/transports/pipe.ts | coding-a,mcp (flipped in branch) | mcp/pipe.go |
| mcp/src/transports/stdio.ts | coding-a,mcp (flipped in branch) | mcp/stdio.go, mcp/stdio_unix.go, mcp/stdio_windows.go |
| mcp/src/transports/streamable-http.ts | coding-a,mcp (flipped in branch) | mcp/sse.go, mcp/streamable_http.go |
| telemetry/src/index.ts | delta-nonchord | telemetry/context.go, telemetry/schema.go, telemetry/schema_json.go |
| telemetry/src/memory.ts | delta-nonchord | telemetry/memory.go, telemetry/memory_now.go |
| telemetry/src/render.ts | delta-nonchord | telemetry/render.go |

## Test rows (`test/parity/interfaces/test-mapping-v1.5.0+c23b9ec1.json`)
| Pi test | owner | Go port on pi-main |
|---|---|---|
| ai/test/abort.test.ts | ai (flipped in branch) | NONE |
| ai/test/anthropic-message-threads.test.ts | ai (flipped in branch) | NONE |
| ai/test/anthropic-strict-tool-schema.test.ts | ai (flipped in branch) | NONE |
| ai/test/anthropic-temperature-compat.test.ts | ai (flipped in branch) | NONE |
| ai/test/clamp-max-tokens.test.ts | ai (flipped in branch) | NONE |
| ai/test/classifier-models.test.ts | ai (flipped in branch) | NONE |
| ai/test/context-overflow.test.ts | ai (flipped in branch) | NONE |
| ai/test/cross-provider-handoff.test.ts | ai (flipped in branch) | NONE |
| ai/test/empty.test.ts | ai (flipped in branch) | NONE |
| ai/test/error-body.test.ts | ai (flipped in branch) | NONE |
| ai/test/generate-models-strict.test.ts | ai (flipped in branch) | NONE |
| ai/test/github-copilot-oauth.test.ts | ai (flipped in branch) | NONE |
| ai/test/image-tool-result.test.ts | ai (flipped in branch) | NONE |
| ai/test/openai-codex-stream.test.ts | ai (flipped in branch) | NONE |
| ai/test/openai-completions-empty-tools.test.ts | ai (flipped in branch) | NONE |
| ai/test/openai-completions-non-null-assistant-content.test.ts | ai (flipped in branch) | NONE |
| ai/test/openai-completions-prompt-cache.test.ts | ai (flipped in branch) | NONE |
| ai/test/openai-completions-thinking-as-text.test.ts | ai (flipped in branch) | NONE |
| ai/test/openai-completions-tool-choice.test.ts | ai (flipped in branch) | NONE |
| ai/test/openai-completions-tool-result-images.test.ts | ai (flipped in branch) | NONE |
| ai/test/openai-responses-compat.test.ts | ai (flipped in branch) | NONE |
| ai/test/openai-responses-prompt-cache.test.ts | ai (flipped in branch) | NONE |
| ai/test/qwen-token-plan-models.test.ts | ai (flipped in branch) | NONE |
| ai/test/retry.test.ts | ai (flipped in branch) | NONE |
| ai/test/stream.test.ts | ai (flipped in branch) | NONE |
| ai/test/supports-xhigh.test.ts | ai (flipped in branch) | NONE |
| ai/test/tokens.test.ts | ai (flipped in branch) | NONE |
| ai/test/tool-call-without-result.test.ts | ai (flipped in branch) | NONE |
| ai/test/total-tokens.test.ts | ai (flipped in branch) | NONE |
| ai/test/unicode-surrogate.test.ts | ai (flipped in branch) | NONE |
| ai/test/validation-probes.test.ts | ai,small (flipped in branch) | NONE |
| ai/test/xai-responses.test.ts | ai (flipped in branch) | NONE |
| codemode/test/declarations.test.ts | delta-nonchord | codemode/declarations_test.go |
| coding-agent/test/agent-session-queued-images.test.ts | delta-nonchord | coding/session_queue_images_test.go |
| coding-agent/test/auth-login.test.ts | delta-nonchord | coding/cli/auth_login_test.go |
| coding-agent/test/auth-logout.test.ts | delta-nonchord | coding/cli/auth_logout_test.go |
| coding-agent/test/auth-storage.test.ts | delta-nonchord | ai/auth_create_test.go, ai/auth_file_creation_test.go, ai/auth_file_lock_cancellation_test.go |
| coding-agent/test/auth-terminal.test.ts | delta-nonchord | coding/cli/auth_terminal_test.go |
| coding-agent/test/bash-executor-truncation.test.ts | delta-nonchord | internal/codingagent/compaction/bash_discarded_output_test.go, internal/codingagent/tools/bash_executor_truncation_test.go |
| coding-agent/test/cache-warmer.test.ts | delta-nonchord | coding/cache_warming_decision_upstream_test.go, internal/codingagent/cache_warmer_test.go |
| coding-agent/test/changelog.test.ts | delta-nonchord | internal/codingagent/changelog_links_test.go, internal/codingagent/changelog_test.go, internal/codingagent/interactive_extension_changelog_test.go |
| coding-agent/test/collapsible-message-components.test.ts | delta-nonchord | tui/collapsible_message_upstream_test.go |
| coding-agent/test/config-selector-keybindings.test.ts | delta-nonchord | coding/cli/config_theme_test.go |
| coding-agent/test/custom-message.test.ts | delta-nonchord | internal/codingagent/custom_message_padding_test.go, tui/custom_message_upstream_test.go |
| coding-agent/test/diff.test.ts | coding-b (flipped in branch) | NONE |
| coding-agent/test/edit-diff-window.test.ts | coding-b (flipped in branch) | NONE |
| coding-agent/test/edit-tool-diff-before-write.test.ts | coding-b (flipped in branch) | NONE |
| coding-agent/test/extension-selector.test.ts | delta-nonchord | tui/extension_selector_test.go |
| coding-agent/test/extensions-runner.test.ts | delta-nonchord | coding/extension/diagnostics_upstream_test.go, coding/extension/host/inproc/chain_dispatch_test.go, coding/extension/host/inproc/upstream_catalog_test.go |
| coding-agent/test/git-update.test.ts | coding-b (flipped in branch) | NONE |
| coding-agent/test/image-resize-worker-teardown.test.ts | coding-b (flipped in branch) | NONE |
| coding-agent/test/interactive-mode-extension-changelog.test.ts | delta-nonchord | internal/codingagent/changelog_test.go, internal/codingagent/interactive_extension_changelog_test.go |
| coding-agent/test/interactive-mode-extension-dialogs.test.ts | delta-nonchord | internal/codingagent/ext_ui_dialog_stack_test.go |
| coding-agent/test/interactive-mode-paste.test.ts | delta-nonchord | internal/codingagent/ext_ui_custom_test.go |
| coding-agent/test/interactive-mode-status.test.ts | delta-nonchord | internal/codingagent/autocomplete_wrappers_test.go, internal/codingagent/custom_focus_upstream_test.go, internal/codingagent/ext_theme_settings_upstream_test.go |
| coding-agent/test/keybindings.test.ts | delta-nonchord | coding/cli/config_theme_test.go, internal/codingagent/custom_editor_keybindings_test.go, internal/codingagent/keybindings_platform_test.go |
| coding-agent/test/llama-extension.test.ts | delta-nonchord | coding/cli/builtin_extension_order_test.go, internal/codingagent/llama/client_test.go, internal/codingagent/llama/huggingface_test.go |
| coding-agent/test/llama-ui.test.ts | delta-nonchord | internal/codingagent/llama/ui_selection_test.go |
| coding-agent/test/mcp-command.test.ts | delta-nonchord | coding/mcpext/cli_carry_test.go, coding/mcpext/cli_test.go, coding/mcpext/cli_upstream_test.go |
| coding-agent/test/mcp-extension.test.ts | delta-nonchord | coding/mcpext/config_test.go, coding/mcpext/connection_test.go, coding/mcpext/servers_section_test.go |
| coding-agent/test/mcp-oauth-refresh.test.ts | delta-nonchord | coding/mcpext/oauth_cimd_test.go, coding/mcpext/oauth_refresh_test.go |
| coding-agent/test/mermaid.test.ts | mermaid (flipped in branch) | NONE |
| coding-agent/test/model-catalog-protocol.test.ts | delta-nonchord | coding/model_catalog_protocol_upstream_test.go |
| coding-agent/test/model-registry.test.ts | delta-nonchord | coding/model_registry_auth_status_upstream_test.go, coding/model_registry_auth_upstream_test.go, coding/model_registry_base_upstream_test.go |
| coding-agent/test/model-resolver.test.ts | delta-nonchord | coding/cli/model_resolver_cli_upstream_test.go, coding/cli/model_resolver_defaults_upstream_test.go, coding/cli/model_resolver_patterns_upstream_test.go |
| coding-agent/test/model-selector.test.ts | delta-nonchord | internal/codingagent/model_selector_errors_upstream_test.go, tui/model_select_refresh_test.go, tui/model_select_test.go |
| coding-agent/test/package-command-paths.test.ts | delta-nonchord | coding/cli/config_builtin_upstream_test.go, coding/cli/main_options_test.go, coding/cli/port_wave_10_helpers_test.go |
| coding-agent/test/package-manager.test.ts | delta-nonchord | coding/cli/npm_command_name_test.go, coding/cli/package_agents_skills_upstream_test.go, coding/cli/package_auto_discovery_upstream_test.go |
| coding-agent/test/plan-mode-utils.test.ts | delta-nonchord | examples/extensions/plan-mode/utils_test.go |
| coding-agent/test/print-mode.test.ts | delta-nonchord | coding/cli/app_mode_binary_test.go, coding/cli/print_mode_upstream_test.go |
| coding-agent/test/prompt-templates.test.ts | coding-b (flipped in branch) | NONE |
| coding-agent/test/provider-matching.test.ts | delta-nonchord | internal/codingagent/provider_matching_test.go |
| coding-agent/test/read-streaming.test.ts | coding-b (flipped in branch) | NONE |
| coding-agent/test/rpc-prompt-response-semantics.test.ts | delta-nonchord | coding/cli/rpc_prompt_semantics_upstream_test.go |
| coding-agent/test/runtime-credentials.test.ts | delta-nonchord | ai/runtime_credentials_settled_test.go, ai/runtime_credentials_upstream_test.go |
| coding-agent/test/subagent-extension.test.ts | delta-nonchord | NONE |
| coding-agent/test/suite/agent-session-codemode.test.ts | delta-nonchord | codemode/memlimit_bench_test.go, coding/codemode_output_summary_test.go, coding/codemode_session_harness_test.go |
| coding-agent/test/suite/agent-session-mcp.test.ts | delta-nonchord | coding/extension/builtin/codemode/reach_describe_test.go, coding/extension/builtin/codemode/run_test.go, coding/extension/builtin/toolsearch/toolsearch_upstream_test.go |
| coding-agent/test/suite/agent-session-model-extension.test.ts | delta-nonchord | coding/session_model_extension_hooks_upstream_test.go, coding/session_model_extension_upstream_test.go, internal/codingagent/session_model_extension_lifecycle_upstream_test.go |
| coding-agent/test/suite/agent-session-queue.test.ts | delta-nonchord | coding/session_queue_images_test.go, coding/session_queue_upstream_test.go |
| coding-agent/test/suite/regressions/11451-thinking-level-after-tree-navigation.test.ts | coding-b (flipped in branch) | NONE |
| coding-agent/test/suite/regressions/11613-bash-timeout-after-shell-exit.test.ts | delta-nonchord | internal/codingagent/tools/bash_timeout_shell_exit_unix_test.go |
| coding-agent/test/suite/regressions/11692-dispose-waits-for-oauth-refresh.test.ts | delta-nonchord | coding/runtime_close_credential_writes_test.go |
| coding-agent/test/suite/regressions/8092-extension-realpath-jiti.test.ts | coding-b (flipped in branch) | NONE |
| coding-agent/test/suite/regressions/8409-aborted-tool-auth-setup.test.ts | coding-b (flipped in branch) | NONE |
| coding-agent/test/suite/regressions/9124-runtime-dispose-tool-settlement.test.ts | delta-nonchord | coding/runtime_dispose_settlement_test.go |
| coding-agent/test/suite/regressions/9154-prompt-during-tree-navigation.test.ts | coding-b (flipped in branch) | NONE |
| coding-agent/test/suite/regressions/9221-reload-during-tool.test.ts | delta-nonchord | coding/cli/headless_reload_test.go, coding/session_reload_busy_test.go |
| coding-agent/test/syntax-highlight.test.ts | delta-nonchord | tui/highlight_test.go, tui/syntax_highlight_theme_upstream_test.go, tui/syntax_highlight_upstream_test.go |
| coding-agent/test/terminal-question.test.ts | delta-nonchord | internal/terminalquestion/terminalquestion_test.go |
| coding-agent/test/tool-search.test.ts | delta-nonchord | coding/extension/builtin/codemode/description_namespace_test.go, coding/extension/builtin/codemode/description_upstream_test.go, coding/extension/builtin/toolsearch/schema_refs_pi_test.go |
| coding-agent/test/tools.test.ts | coding-b (flipped in branch) | NONE |
| coding-agent/test/tree-selector.test.ts | delta-nonchord | internal/codingagent/tree_selector_upstream_test.go, tui/tree_select_empty_state_test.go, tui/tree_select_test.go |
| coding-agent/test/truncate-surrogates.test.ts | coding-b (flipped in branch) | NONE |
| durable/test/codemode-models.test.ts | durable | durable/codemode/models_test.go |
| durable/test/codemode.test.ts | durable | codemode/memlimit_bench_test.go, coding/codemode_output_summary_test.go, coding/codemode_session_harness_test.go |
| durable/test/harness-after-run.test.ts | durable | durable/core/driver/harness_lanec_core_test.go, durable/harness/harness_after_run_test.go |
| durable/test/harness-cancellation-barrier.test.ts | durable | durable/core/session/sched_restart_test.go, durable/harness/harness_cancellation_barrier_test.go |
| durable/test/harness-compaction.test.ts | durable (flipped in branch) | NONE |
| durable/test/harness-context.test.ts | durable | durable/harness/context_extend_test.go, durable/harness/context_lead_test.go, durable/harness/harness_context_at_test.go |
| durable/test/harness-generation.test.ts | durable (flipped in branch) | NONE |
| durable/test/harness-group-commit.test.ts | durable | durable/core/group_commit_test.go, durable/harness/harness_group_commit_support_test.go, durable/harness/harness_group_commit_test.go |
| durable/test/harness-lifecycle.test.ts | durable | durable/core/driver/harness_lifecycle_core_test.go, durable/harness/harness_lifecycle_test.go, durable/session/session.go |
| durable/test/harness-nested-tools-restart.test.ts | durable | durable/core/driver/tool_nested_restart_core_test.go, durable/harness/tool_nested_records_test.go, durable/harness/tool_nested_restart_test.go |
| durable/test/harness-nested-tools.test.ts | durable | durable/core/driver/harness_nested_core_test.go, durable/core/driver/harness_nested_slots_core_test.go, durable/harness/tool_nested_calls_doc_test.go |
| durable/test/harness-observers.test.ts | durable | durable/core/session/sched_registry_blocked_test.go, durable/harness/harness_observers_test.go |
| durable/test/harness-partial-changes.test.ts | durable | durable/harness/partial_changes_upstream_test.go |
| durable/test/harness-registry-reader.test.ts | durable | durable/harness/registry_reader_host_test.go, durable/harness/registry_reader_test.go |
| durable/test/harness-storage-failure.test.ts | durable | durable/core/driver/safe_report_core_test.go, durable/harness/harness_listener_errors_test.go, durable/harness/harness_storage_failure_test.go |
| durable/test/harness-structured-output.test.ts | durable | durable/core/driver/harness_nested_core_test.go, durable/core/driver/tool_structured_callers_core_test.go, durable/harness/tool_nested_records_test.go |
| durable/test/harness-structured.test.ts | durable | durable/core/session/sched_wait_test.go, durable/harness/harness_structured_test.go |
| durable/test/harness-submit-in-commit.test.ts | durable (flipped in branch) | NONE |
| durable/test/harness-task-graph.test.ts | durable | durable/core/driver/harness_task_graph_core_test.go, durable/harness/harness_task_graph_test.go |
| durable/test/harness-tasks-recovery.test.ts | durable | durable/harness/harness_tasks_recovery_test.go, durable/harness/harness_tasks_times_test.go |
| durable/test/harness-tasks.test.ts | durable | durable/core/driver/fault_tasks_core_test.go, durable/core/driver/harness_tasks_core_test.go, durable/core/driver/harness_tasks_more_core_test.go |
| durable/test/harness-tool-hooks.test.ts | durable | durable/harness/harness_tool_hooks_test.go |
| durable/test/harness-tools-recovery.test.ts | durable | durable/core/turn/commit_progress_test.go, durable/harness/harness_coding_tools_test.go, durable/harness/harness_tools_recovery_group_commit_test.go |
| durable/test/harness-tools.test.ts | durable | chord/output/output.go, durable/core/driver/harness_provider_request_core_test.go, durable/harness/agent_regression_test.go |
| durable/test/harness-view-run.test.ts | durable | durable/core/driver/generation_abort_core_test.go, durable/core/driver/tool_structured_callers_core_test.go, durable/harness/harness_view_run_test.go |
| durable/test/indexeddb-storage.test.ts | durable | NONE |
| durable/test/jsonl-storage.test.ts | durable | durable/storage/backend_reads_test.go, durable/storage/jsonl/jsonl_storage_test.go, durable/storage/jsonl/poisoned_storage_test.go |
| durable/test/session-adoption.test.ts | durable | durable/session/session_adoption_internal_test.go, durable/session/session_adoption_jsonl_test.go, durable/session/session_adoption_test.go |
| durable/test/session-cached-storage.test.ts | durable | durable/session/cached_storage_test.go |
| durable/test/session-deferred-commits.test.ts | durable | durable/core/driver/session_deferred_commits_core_test.go, durable/core/session/held_test.go, durable/session/session_deferred_commits_upstream_test.go |
| durable/test/session-definitions.test.ts | durable | durable/session/session_definitions_test.go |
| durable/test/session-documents.test.ts | durable | durable/core/driver/docs_core_test.go, durable/session/session_documents_test.go, durable/session/subscribe_close_test.go |
| durable/test/session-group-commit.test.ts | durable | durable/core/group_commit_test.go, durable/core/session/writer_fanout_test.go, durable/session/group_commit_support_test.go |
| durable/test/session-observers.test.ts | durable | durable/core/driver/docs_core_watches_test.go, durable/session/session_observers_test.go |
| durable/test/session-tables.test.ts | durable | durable/core/driver/docs_core_tables_test.go, durable/core/hops_host_refused_test.go, durable/session/session_tables_test.go |
| durable/test/session-watches.test.ts | durable | durable/core/driver/docs_core_watches_test.go, durable/session/session_watches_test.go |
| durable/test/spec-usage.test.ts | durable | durable/examples/spec_usage_upstream_test.go, durable/spec_usage_upstream_test.go |
| durable/test/sqlite-commit.test.ts | durable (flipped in branch) | NONE |
| durable/test/sqlite-storage.test.ts | durable (flipped in branch) | NONE |
| durable/test/store-storage-commit.test.ts | durable | durable/session/store_storage_publication_test.go, durable/storage/store/store_storage_commit_test.go |
| durable/test/store-storage.test.ts | durable | durable/storage/store/store_storage_host_test.go, durable/storage/store/store_storage_test.go |
| durable/test/tool-search.test.ts | durable | coding/extension/builtin/codemode/description_namespace_test.go, coding/extension/builtin/codemode/description_upstream_test.go, coding/extension/builtin/toolsearch/schema_refs_pi_test.go |
| durable/test/tools.test.ts | durable | ai/google_convert_tools_upstream_test.go, ai/openai_test.go, chord/output/output.go |
| durable/test/types.test.ts | durable | ai/catalog_options_upstream_test.go, ai/message_types_upstream_test.go, ai/models_typed_upstream_test.go |
| mcp/test/auth-provider.test.ts | coding-a,mcp (flipped in branch) | NONE |
| mcp/test/config.test.ts | coding-a,mcp (flipped in branch) | NONE |
| mcp/test/input-schema.test.ts | coding-a,mcp (flipped in branch) | NONE |
| mcp/test/lines.test.ts | coding-a,mcp (flipped in branch) | NONE |
| mcp/test/loopback.test.ts | coding-a,mcp (flipped in branch) | NONE |
| mcp/test/naming.test.ts | coding-a,mcp (flipped in branch) | NONE |
| mcp/test/oauth.test.ts | coding-a,mcp (flipped in branch) | NONE |
| mcp/test/output-limit.test.ts | coding-a,mcp (flipped in branch) | NONE |
| mcp/test/pipe.test.ts | coding-a,mcp (flipped in branch) | NONE |
| mcp/test/sign-in.test.ts | coding-a,mcp (flipped in branch) | NONE |
| mcp/test/stdio.test.ts | coding-a,mcp (flipped in branch) | NONE |
| mcp/test/streamable-http.test.ts | coding-a,mcp (flipped in branch) | NONE |
| telemetry/test/render.test.ts | delta-nonchord | internal/codingagent/interactive_keyboard_render_test.go, internal/codingagent/interactive_tool_lifetime_test.go, telemetry/render_upstream_test.go |
| telemetry/test/store.test.ts | delta-nonchord | ai/models_store_pi_lock_test.go, ai/models_store_read_upstream_test.go, ai/models_store_upstream_test.go |

## Flipped on fix/delta-nonchord-1 (this branch)
Source rows (18): telemetry index/memory/render, codemode declarations/index, core/keybindings, cli/config-selector, core/bash-executor, core/tools/bash, find, ls, core/runtime-credentials, utils/changelog, extensions/codemode/execute, extensions/mcp/index, modes/rpc/rpc-mode, utils/web-url, components/login-dialog.
Test rows (8): telemetry render+store, codemode declarations, config-selector-keybindings, bash-executor-truncation, 11613, 11692.
Remaining `delta-nonchord` rows (30 src, 42 tests) need a per-row check of the Go test against the Pi delta before flipping; split them by package (coding-agent core/tui/interactive, coding-agent extensions, ai/agent, tests).

## Lane rules
- Each lane flips, with its merge, the rows in its owner column (src row: `disposition="ported"`, `evidence=["test:<go file>#<Test>"]`, delta-specific `rationale`; test row: `disposition="ported"`, `evidence`, `rationale`, keep `upstreamTestHash`). Rows listed "(flipped in branch)" already are; keep them when merging pi-main.
- durable: 59 src + 34 test rows. 45 src rows are only chord import-path changes (`@earendil-works/chord` to `/context`, `/json`, `/delta`, `/watch`): `unchanged-observable` or `designed-out` with that rationale. Real deltas: harness/agent, compaction, generation, types (per-conversation compaction, stream options), session/transaction (Tx.Submission), storage/sqlite/{commit,rows,statements,storage} (batch commit), tool-search (schema $ref), registry-reader. `indexeddb-storage.test.ts` is designed-out (browser-only).
- coding-a: auth-login/logout/terminal/command, radius-mcp, llama/ui, tool-search tool, main.ts, interactive-mode.ts, terminal-question, mcp-servers, mcp/cli.
- coding-b: agent-session, package-manager, prompt-templates, tools edit/read/grep/edit-diff, lazy.ts, models.ts, diff component, image-resize.
