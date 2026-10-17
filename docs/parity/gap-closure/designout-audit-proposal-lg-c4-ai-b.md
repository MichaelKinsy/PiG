# Design-out audit proposal, lg-c4-ai-b (parked for the lead; not the live file)

`test/parity/interface-closure/breakdown/proofclass.py` counts a designed-out row as unproven until `docs/parity/designout-audit.tsv` has parity, maintainability and performance columns for it (the file is absent on integrate, so every design-out row is unproven: 183 rows in agent, ai, chord, client, env, mcp and telemetry at 660 of 12324 overall).

`designout-audit-proposal-lg-c4-ai-b.tsv` here is the same layout the script reads (`id`, `basis`, `parity`, `maintainability`, `performance`) for the 155 rows whose existing rationale falls into one of five classes, each row with the class's audit text taken from that rationale:

| basis | rows | what the audit says |
|---|---|---|
| a: no Pi reader | 44 | telemetryContext is declared and only copied in Pi 1.1.0; Go copies it too and fabricates no read |
| D103 | 28 | Error.stack is a JavaScript runtime capture nothing reads |
| language mechanics: type-level only | 58 | conditional, mapped and lookup types, declaration merging, type re-exports: no runtime value |
| language mechanics: AbortSignal | 8 | cancellation is the context.Context parameter of the same operations |
| probe: no ErrorOptions | 17 | no constructor passes ErrorOptions, so cause is never set |

Audit judgments are reviewed inputs, so this is a proposal: nothing is written to `docs/parity/designout-audit.tsv`. To adopt it, append the rows (without the header) to that file with its own header. The other 28 design-out rows need a row-specific audit and are not covered:

- `pkg:agent/.#AgentTool::property:replay`
- `pkg:ai/.#JsonValue`
- `pkg:ai/.#ProviderId`
- `pkg:ai/api/bedrock-converse-stream.lazy#setBedrockProviderModule`
- `pkg:ai/api/bedrock-converse-stream.lazy#setBedrockProviderModule::call:0`
- `pkg:ai/api/cloudflare-ai-binding#AiBinding::property:aiGatewayLogId`
- `pkg:ai/bedrock-provider#bedrockProviderModule`
- `pkg:ai/bun-oauth#registerBunOAuthFlows`
- `pkg:ai/bun-oauth#registerBunOAuthFlows::call:0`
- `pkg:ai/compat#BuiltinProvider`
- `pkg:ai/compat#JsonValue`
- `pkg:ai/compat#ProviderId`
- `pkg:ai/compat#setBedrockProviderModule`
- `pkg:ai/compat#setBedrockProviderModule::call:0`
- `pkg:ai/providers/all#BuiltinProvider`
- `pkg:chord/.#Context`
- `pkg:chord/.#Context::property:abortSignal`
- `pkg:chord/.#ContextKey::property:token`
- `pkg:chord/.#ContextKey::property:valueType`
- `pkg:chord/.#CopyJsonOptions`
- `pkg:chord/.#Service::property:%5BSymbol.SERVICE_TYPE%5D`
- `pkg:chord/.#copyJson::call:0`
- `pkg:chord/delta#NonEmptyPath`
- `pkg:chord/delta#PathRef`
- `pkg:mcp/oauth#AddClientAuthentication::call:0`
- `pkg:telemetry/.#ExactTelemetryAttributes`
- `pkg:telemetry/.#TelemetrySchemaSpanName`
- `pkg:telemetry/.#createTypedSpanStarter::call:0`
