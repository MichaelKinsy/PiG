### Fixed

- Reloading an evicted history chunk now validates each record length with `checkRecordSize` before narrowing to `uint32`, preventing a corrupted or oversized loader record of length `n + k*2^32` from bypassing the entry length check in `Chain.fill`. <!-- fix: af138c9a84 --> <!-- cause: Chain.fill compared c.ents[i].n != uint32(len(rec)) without first checking that len(rec) fits within maxRecordBytes -->
