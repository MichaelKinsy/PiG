### Fixed

- An extension that registers a provider override for a built-in provider (`pi.registerProvider("anthropic", { streamSimple })`) and then reads `ctx.modelRegistry.getProvider()` now sees the registration at once, as in Pi. After it unregisters the provider, the same reads see the built-in or models.json provider at once. `pi-background-tasks` 2.6.9 no longer stops startup with `pi_anthropic_attribution_install_failed`.
