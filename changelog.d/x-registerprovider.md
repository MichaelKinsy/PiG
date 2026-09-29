### Fixed

- An extension's `registerProvider` or `unregisterProvider` now updates the shared available-models snapshot before the call returns, as in Pi. Model cycling, SDK `GetAvailableSnapshot` readers and registry availability see a configured or stored provider at once, and removed providers disappear at once. The changed provider's availability is then re-checked in the background.
