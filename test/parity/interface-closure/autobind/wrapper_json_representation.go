package main

// JsonRepresentation<T> (chord types.ts:26) is T as strict JSON, which Go carries as the concrete Go type of T (lead ruling 2026-10-07 15:20 MDT), so the checker reads it as its argument like the other wrapper types in wrapperGen.
func init() { wrapperGen["JsonRepresentation"] = true }
