# Equivalence of the TypeScript runtimes on the ten pigbench extensions, 2026-10-11

Tip: integrate-042 3352c45971 plus bench/bolt-aot at the commit that adds this file; Node (system), Bun 1.4.2 (stock slot), aot (Pi-Bolt 0.7.4 compiler, one executable for the ten extensions, built by the Piglet's worker). Extensions: `bench/evals/fixtures/extensions/bench-ext-01..10.ts` (the print-ext10 / rpc-ext10 set). Run: `python3 bench/bolt-aot/equivalence.py` (usage in its header), 30 scripted RPC steps per arm: get_commands, the tools the first model request declares, a prompt per extension whose scripted model calls `bench_tool_NN` (result content and details recorded), the ten `/bench-NN` commands, and ten more tool calls that read every handler's cumulative event count (3 after the first call, 79 after the last). Each section is canonical JSON, hashed (first 16 hex digits of SHA-256); the arms are equivalent when every hash equals. node2 repeats node as the A/A control.

Arm names for lane ext-matrix: **pig TS on Node** = `PIG_TS_RUNTIME=node` (pig-evals arm `pig-ts-node`), **Bun Piglet arm** = `PIG_TS_RUNTIME=bun` (`pig-ts-bun`), **pig-pi-bolt** = `PIG_TS_RUNTIME=aot` (`pig-ts-aot`: the compiled-extensions Piglet; its image for the set is built by pig-evals' warm_aot, or here by the Piglet's worker).

| section | node | node2 | bun | aot |
|---|---|---|---|---|
| calls | 55c49fdc749911e8 | 55c49fdc749911e8 | 55c49fdc749911e8 | 55c49fdc749911e8 |
| commands | 76a5055d81379529 | 76a5055d81379529 | 76a5055d81379529 | 76a5055d81379529 |
| first_request_tools | 9ad6e860a55ef908 | 9ad6e860a55ef908 | 9ad6e860a55ef908 | 9ad6e860a55ef908 |
| tool_messages_to_the_model | 46f43d60b1463ef1 | 46f43d60b1463ef1 | 46f43d60b1463ef1 | 46f43d60b1463ef1 |
| extension host | node | node | bun | pig-ts-host |
| equals node | True | True | True | True |

Result: all four columns are byte-identical in every section, and each arm ran the tier it names (extension host process node / node / bun / pig-ts-host, asserted per run). Mutation check: changing one extension's output text ("bench 05 ok" to "bench 05 OK") changes the `calls` and `tool_messages_to_the_model` hashes, so the comparison would have caught a difference.

What this proves and does not: the observable extension surface (command list with descriptions, declared tool schemas as the model sees them, tool results and details, command responses, handler run counts) is the same on all three runtimes for this set. It does not cover timing, memory or the Bun/aot identity differences listed in the Piglet README's known limits.
