### Changed

- A provider re-registration with `insecure: false` now turns certificate checks back on for that provider's endpoint. Before, only unregistering the provider did: a re-registration could set `insecure: true` but not undo it, a rule that shipped in 0.5.0 and held through 0.7.0. A re-registration that leaves `insecure` out still keeps the earlier value, as Pi's `registerProvider` keeps any value a re-registration leaves undefined (`model-runtime.ts:970-977`). Every extension SDK says it the same way: the `insecure` member of the provider config, absent, `false` or `true`. The Go extension API keeps `ProviderConfig.Insecure bool` (true is on, the zero value is omitted) and adds `InsecureSetting()` for the three states.

### Security

- A user or extension that registers a provider again with `insecure: false` now gets certificate verification back for that provider. Before, the earlier `insecure: true` stayed in force until the provider was unregistered.
