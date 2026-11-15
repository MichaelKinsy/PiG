### Changed

- chord: a client and a page can set the limits per part of what arrives from the server (Pi 34fb59f67). `surfaces.ClientOptions.Limits` (`*link.PartLimits`: `Part` bytes on the wire, `Decoded` bytes after inflating) and the page's `limits` option take them; without them a client keeps an untrusted link's limits, 16 MiB on the wire and 64 MiB decoded. A part past a limit fails as on any link with limits: a client that cannot take the scripts object's set keeps the code it started with.
