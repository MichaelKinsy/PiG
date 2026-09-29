<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# fin-runtime-surface corpus pins and print exits

These are load/print observations, not full compatibility results. See [the slice report](fin-runtime-surface.md) for command diagnostics and remaining blockers.

Pi: `0.87.1`. Baseline: `da3b0ff`.

- Package-list SHA256: `5b19b7466612b12979e861f07906a4c6ca2d632de8afe9e13a736636faa85ce8`
- Baseline-report SHA256: `e9b720ffee9259b0e3b9a69a69eb2dbdf63d14305acbe7df6bb27a5b92be6eca`
- Candidate binary SHA256: `dcc165cd79f3f60fdc4c12c504004b8eff5234f098f756c545f84b7a61119d6a`

| Package | Version | Pi print exit | PiG print exit | Package manifest SHA256 |
|---|---|---|---|---|
| `pi-mcp-adapter` | 2.38.0 | 0 | 0 | `ef9b7355d2c8bf360ed8cd43b31170d5c75e9a9e6fec3b16b643fa08fd2606c2` |
| `pi-subagents` | 0.71.0 | 0 | 0 | `7c978160eacba4b8b58c838401dd8d2919bbc3a2fd8e8f1673e580ee5659e7f9` |
| `@langfuse/pi-observability-plugin` | 0.1.2 | 0 | 0 | `4d430f5cb845191547a3b9e6da911e77bb4f4ef7f245926fe0bd3212bb0bc186` |
| `pi-web-access` | 0.31.0 | 0 | 0 | `05eae6e1858d040f1bd1fdaaaaf3d0c892e58e6d8855899d12b28c23d3dfee46` |
| `billion-context` | 0.1.158 | 0 | 0 | `64e96d56078344747320e2e3dacce053e2a4849ed7931c37dde62a5ac55d14c2` |
| `@juicesharp/rpiv-ask-user-question` | 2.11.0 | 0 | 0 | `df9ed35aedf07e038736186722cca76c0cc6ebf43d62d5d1a05bf48b002f087c` |
| `pi-mcp-extension` | 1.5.0 | 0 | 0 | `b25cb29156bda4fdc564525ca87c2a6824bcdf94387d59caf5891370009d46d2` |
| `@juicesharp/rpiv-todo` | 2.11.0 | 0 | 0 | `2c7c1dd12a9935fff4c21f43faaf7d705c72c42f9b7891517c5ea0073e480f7f` |
| `@companion-ai/feynman` | 0.5.8 | 0 | 0 | `3b837fbfed9b15bc851ecaa6c1b7d4fdb8e09e8fec6fc0551387e4d09c721a8a` |
| `pi-goal-x` | 0.31.9 | 0 | 0 | `cded3ae52d263ba0b4fa074a751797125595cb33e3ac90202b0e6b5f85341f3c` |
| `@plannotator/pi-extension` | 0.27.20 | 0 | 1 | `c24a61c018d7464730f54f4e82dcc39303e4bb1454f58f2c341036f0aa8cac30` |
| `bigpowers` | 2.88.9 | 0 | 0 | `2997620bf0e1e0096004e63528e7be5e4465651dc47aefb2f57bb73d3d6e1b24` |
| `pi-lens` | 4.3.0 | 0 | 0 | `9aa0c8bd5507008dd9cf579b6b938bc219e86ccb627936084e0ea784290c81dc` |
| `pi-powerline-footer` | 0.18.0 | 0 | 0 | `be1485f6cd65d19777c0d2d10f242afcb396fc7b17487b010b92425196fca5d3` |
| `@narumitw/pi-usage` | 0.61.1 | 0 | 0 | `0136a17b4d345a3921a1dc70073de97ef5e510fff05391889aa11be639e9bbd4` |
| `@dietrichgebert/ponytail` | 4.10.0 | 0 | 0 | `2a45fda7a379871c5e6e49636989766f304a5dd77931cd70869669272401e3c5` |
| `context-mode` | 1.0.169 | 0 | 1 | `f0a96c5affd66f5334f2f4cd5a1270e65018a0b83317b4e752cd6f60cdba8680` |
| `pi-claude-bridge` | 0.8.0 | 0 | 0 | `abc4ee9b1933fdd857e65332643994b095cc8066664f3d19efca5e98d8f30ba4` |
| `pi-cc-extensions` | 0.9.5 | 0 | 0 | `82a946bfba7e26969658b491e314223a218cee2e5fcaa1a60eafcca18d5eb531` |
| `@moyai/pi-session-hoarder` | 0.2.0 | 0 | 0 | `943a358412800d417f193c33e99a953adb2dc9e09091f0a752b8ba787493ac98` |
| `agent-comms` | 8.0.8 | 0 | 1 | `9406b0559ed9f69c5387a02d8fb510e563a85f5452ceb59bad96e16b2a58514f` |
| `@henryqw/pi-subagent` | 22.2.3 | 0 | 0 | `6d8a7936823f9f292f87dee43a2b39c17718bb80fd3d85314a65f779c2baf6a2` |
| `@langchain/langsmith-pi-extension` | 0.2.0 | 0 | 0 | `fe86caee905874d4adc6f562ad1ae6b2e731ab11c9747bb372015bc294c45d34` |
| `@gotgenes/pi-permission-system` | 34.0.1 | 0 | 0 | `34516314bc8d282217af01829af52cd49de5ff156f17b4a310fa487426a90c79` |
| `billion-context-pi` | 0.1.81 | 0 | 0 | `c8328a391b1d55a8129cec1a9318e942c62a6b8ea385d4eff23e993a029059c9` |
| `@raindrop-ai/pi-agent` | 0.2.4 | 0 | 0 | `a0234e7378757a9aed1ba3f280e46cda01501af26a96422f523a21e742cb26c9` |
| `@schovest/pi-goal` | 0.2.0 | 0 | 0 | `f606a5b19ef5c3cee4779f792e5d30b7df9746cc7d0f2870fb8086faca3d1a2d` |
| `@ff-labs/pi-fff` | 0.11.0 | 0 | 0 | `d0ea0e3f5e36b99205119d35246de693d38bac531e63bc911716af1512126284` |
| `pi-advisor-flow` | 0.8.2 | 0 | 0 | `83f31b79cd23d459428dd95dce2b3f59fcbe20ee1a9aeda5b10f0c96a323e72f` |
| `@henryqw/pi-task-models` | 7.0.2 | 0 | 0 | `e5165c368efb7439d4487587ffdbcf85cfd9f255bf8fe3bc498019adccacf1b3` |
| `gentle-pi` | 3.7.0 | 0 | 0 | `cf82bc8e3fb07fddfdae5f73a78dd4d17a9d2198e447e129c0430a2887145462` |
| `pi-simplify` | 0.2.3 | 0 | 0 | `8960262848ea475bef8645e5d5ed66f82fa639d8ecf6511882f056d6f25570f1` |
| `confluence-cli` | 2.25.2 | 0 | 0 | `6f4c726adc617deba3cfc5109b454f600d0529aa5a720643daddedd9fb701a1e` |
| `pi-btw` | 0.6.1 | 0 | 0 | `4bed98c5c4cf8c9ce240c45c5c38a5b0ab8ee45643d4c9a1580d69d83d44e440` |
| `@kontextmind/kxm` | 0.7.131 | 0 | 1 | `72791f57b3588db2bccd96e6867a5b6fa4498971959247bf37ccf2b7e01f6911` |
| `@akagilnc/pi-workflow-roles` | 0.1.5202 | 0 | 0 | `ead9be84a3049de13f368ec6bb867fbac608cd2cb558d9665c90fd121337bb35` |
| `@agimon-ai/log-sink-mcp` | 0.29.31 | 0 | 0 | `f5ce0416638502a00cc7f04ccbbf0f0253f807dc0d35944b508d787ea054275b` |
| `pi-memory` | 0.4.2 | 0 | 0 | `878519a29616a564058e693df118086962ec4225d79ea49ada40c6edcaaba20b` |
| `@narumitw/pi-btw` | 0.61.1 | 0 | 0 | `ee92a2aa9c6393efd73112d3e7797731264e269a01ecfb37086b7cf0de954330` |
| `@narumitw/pi-goal` | 0.54.8 | 0 | 0 | `b3ff8c1af92d9d3af40e8bb1ad57faeb7c0e40ce87f9304fcc59c41724464529` |
| `pi-vim` | 0.14.2 | 0 | 0 | `7d15a1ddccf7c81e4a9843248e80d4f6130583c1c1fedc7e2aba708ec20fda31` |
| `pi-rtk-optimizer` | 0.9.0 | 0 | 0 | `c70edc208e9b898baa657489a36c8a0c8c7d88d30bfa2ef3758c80c04d2bc314` |
| `pi-auto-update` | 0.1.4 | 0 | 0 | `af6ae2c6dfcd9b7d8b2415ce03263d08ea7f3070a8deafebdcb9188cfcf4a740` |
| `@upstash/context7-pi` | 0.1.2 | 0 | 0 | `367f6565087be5e89315d3cb171d9f391017124b598b744662c3500705adcb11` |
| `pi-hermes-memory` | 0.9.9 | 0 | 0 | `c73db9c95cb64990dcb9bd071e007ecc6786dfab2283f835bf882cd58b2b5358` |
| `pi-msg-queue` | 1.0.2 | 0 | 0 | `768156b27bbdcff038bea8c2f432fba7e9eac46765dda9042bc3754f82a1c0fd` |
