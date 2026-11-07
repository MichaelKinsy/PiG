### Changed

- The `pig` binary is about 0.42 to 0.60 MB smaller (linux_amd64 0.60 MB): sorts of short lists and of lists that no timed path sorts at length use one shared sort routine instead of one copy of the sort per element type. Sorts of long lists on timed paths (session load, the model picker, file suggestions) keep the standard library's code, so no sort on a timed path takes longer. The sorted order is the same.
