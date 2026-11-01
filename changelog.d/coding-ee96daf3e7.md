### Fixed

- Unsubscribing an extension's event-bus subscription is now constant time, as deleting from Pi's `Set` of tracked unsubscribes is. The runtime removed each one from a slice, so unsubscribing 10,000 subscriptions one at a time took 142 ms and the cost grew with the square of their number; it now takes under 1 ms. The factory API also kept every subscription an extension had made, unsubscribed or not, until the extension reloaded; it now keeps only the live ones. <!-- fix: ee96daf3e7 -->
