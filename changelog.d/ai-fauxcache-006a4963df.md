### Fixed

- The faux provider no longer counts newly cached prompt tokens as input as well as cache writes: with a session every prompt token is a cache read or a cache write. <!-- fix: 006a4963df -->
