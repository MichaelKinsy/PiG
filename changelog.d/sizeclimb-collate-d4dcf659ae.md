### Changed

- The `pig` binary is about 1.3 MB smaller: string ordering (`a.localeCompare(b)` parity: model lists, the `ls` tool, package and server lists) uses a fork of the root-locale collation of `golang.org/x/text` with only the tables the root locale reads, not the tables of every locale. The order of every string is the same: the fork is tested against x/text for every code point and for random strings. On the `ls` benchmark with 10,000 names, a sort takes 16.3 ms and allocates 2.60 MB, where it took 17.8 ms and 5.52 MB.
