### Fixed

- durable: the task graph's mount (the records and the value the blocked reasons, the commits' publications and the observers' attachment each read or change) is one turn at a time, as Pi's synchronous turns are: a registry change refreshing the blocked reasons while a publication advanced the graph read and wrote the mount from two goroutines (a data race, seen as a worker exit 66 in the p2 TUI host tests under load), and could tell the observers their revisions out of order. <!-- fix: p2-tui-race -->
