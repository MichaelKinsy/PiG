### Fixed

- An extension provider's model that another provider also catalogs, such as `gpt-6-astra` under `azure`, no longer takes that provider's API when it is built before its own provider registers. Before this fix, such a model took the other provider's API and the turn failed (`LiteLLM model gpt-6-astra declares unsupported protocol "azure-openai-responses"`). `BuildModel` now matches only the requested provider's catalog, as the CLI's startup resolution already did. <!-- fix: 366ae8eb45 -->
