### Changed

- A session without a file (`--no-session`, RPC and print runs) writes the entries it compacted away to an unnamed temporary file instead of keeping their compressed records in memory. The file has no name while it is open and is gone when pig exits. Entries read back unchanged for `/tree`, branching and export. Live memory per exchange of a long session falls from about 1.5 KB to about 0.6 KB.
