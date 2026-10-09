# MCP client conformance

Runs the official [MCP conformance suite](https://github.com/modelcontextprotocol/conformance) against PiG's MCP client and compares the result with a committed baseline. Ports `packages/coding-agent/test/mcp-conformance` of Pi.

```bash
make test-mcp-conformance
```

It needs network access the first time, to fetch the pinned suite (`@modelcontextprotocol/conformance@0.2.0-alpha.11`) with `npx`. Install scripts are disabled. It needs a POSIX shell. It is not part of `make check`, because it depends on the network.

## How it works

For every protocol version PiG negotiates (`2025-03-26`, `2025-06-18`, `2025-11-25`), `run` lists the suite's client scenarios and runs them one at a time. The suite starts a scenario server and runs `client` against it.

`client` uses the code PiG runs for an HTTP server in `mcp.json`: `mcpext.Connection` connects and calls tools, and `mcpext.SignInMcpServer` runs the OAuth sign-in that `/mcp` starts. A simulated browser fetches the authorization URL and delivers the redirect to PiG's loopback callback server. When the server asks for sign-in again (for example for more scope), the simulated user signs in again, up to three times.

Every check the suite reports is compared with `baseline.json`. The extra `pi-client` check records whether `client` completed the scenario. The id is Pi's, so the two baselines compare line by line. Some scenarios expect the client to give up (`auth/scope-retry-limit`), so it is baselined like the others.

The run fails when:

- a check that passes in the baseline fails or is missing,
- a check fails that the baseline does not list as failing,
- a baselined scenario did not run.

Checks that started passing are reported. Update the baseline to lock them in.

`baseline.json` is identical to Pi 1.0.4's. The known failures are the same:

- `elicitation-sep1034-client-defaults`: PiG does not support elicitation.
- `auth/basic-cimd`: PiG registers clients dynamically instead of using a Client ID Metadata Document.
- `auth/scope-retry-limit`: `pi-client` fails by design; the server never accepts the granted scope.

2026-07-28 is not covered: it is a stateless protocol PiG does not implement.

## Options

```bash
go run ./test/mcp-conformance/run --mode 2025-11-25 --scenario auth/scope-step-up --verbose
go run ./test/mcp-conformance/run --update-baseline
make test-mcp-conformance MCP_CONFORMANCE_ARGS="--mode 2025-11-25"
```

- `--mode`, `--scenario`: run a subset (repeatable).
- `--verbose`: print the suite's output, including each check and the client's log.
- `--keep-results`: keep the suite's `checks.json` and client output.
- `--update-baseline`: write the results of a full run to `baseline.json`. Review the diff. Do not regenerate it to hide a regression.
