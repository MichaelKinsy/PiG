### Fixed

- Device-code logins (GitHub Copilot, Kimi Code, Meta, Radius) use the `verification_uri` a server sends only when it is an `http:` or `https:` URL, and open and show its normalized form, so a malicious login server cannot make `pig` launch a `file:` or custom protocol URL. Ports Pi's `trustedHttpUrl` use in every login that opens a server-supplied URL. <!-- fix: 439c708ebc -->
- A pi-messages event whose `contentIndex` is not an integer naming the next block (for a `_start` event) or an existing block (for any other event) now fails with `Invalid contentIndex <value> in pi-messages <type> event`, as Pi's does, instead of indexing the message's content with a value from the wire. <!-- fix: 439c708ebc -->
