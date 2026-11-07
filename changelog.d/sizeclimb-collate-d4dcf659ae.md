### Changed

- The `pig` binary is about 1.3 MB smaller: string ordering (`a.localeCompare(b)` parity: model lists, the `ls` tool, package and server lists) uses a fork of the root-locale collation of `golang.org/x/text` with only the tables the root locale reads, not the tables of every locale. The order of every string is the same: the fork is tested against x/text for every code point and for random strings. The `ls` tool sorts a directory of 10,000 names about 7% faster and allocates about 45% less.
