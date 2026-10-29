### Fixed

- After `pig update --models`, OpenRouter's `:nitro` and `:floor` routing variants no longer disappear from `--list-models`, the model selector, `/scoped-models`, RPC `get_available_models` and the SDK lists. The refreshed list replaces the bundled OpenRouter list, and the variants were derived from the bundled models only; they are now derived from whichever list is current (D199). <!-- fix: cb20ec03cd -->
