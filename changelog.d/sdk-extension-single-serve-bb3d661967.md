### Fixed

- An extension served in-process now refuses to serve a second connection while it serves one, with the error `this extension is already serving a connection; an Extension serves one connection at a time, so build a new one per connection`. A second serve of the same `sdk.Extension` used to overwrite the first one's state and leave the second host waiting for a registration that never came. <!-- fix: bb3d6619673bfea35715c93dd637b2c7bb98dd67 -->
