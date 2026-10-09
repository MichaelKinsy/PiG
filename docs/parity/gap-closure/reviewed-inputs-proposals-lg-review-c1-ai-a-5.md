# Parked reviewed input from lg-c4-ai-a-5 (review of 02261945a)

Under the reviewer policy shared by lg-review-c1/c2/c3, lane-added entries in the reviewed-input files wait for lead approval. The entry below was removed from `test/parity/interface-closure/autobind/reviewed-types.json`; pkg:durable/.#Tx, Tx.doc and Tx.doc::call:0 return to pending.

## `pkg:durable/.#Tx::property:doc::call:0` -> `durable/types.go#Tx.Doc` (from 02261945a)

```json
{
 "go": "durable/types.go#Tx.Doc",
 "reason": "Pi durable/src/types.ts:804-811 doc<T extends JsonObject>() resolves to Draft<T> (chord/src/delta/draft.ts:2), a Proxy that records writes to a JSON object as a delta. Go cannot intercept field assignment on a typed value, so the same transaction-scoped mutable JSON view is the handle *delta.Object (chord/delta/handles.go:15), with Get/Set over the JSON members T declares; as for TB2 and T13, the JSON-valued type parameter is carried untyped. Every other overload part (owner ID, family key, seed) is checked by the variadic rule."
}
```

Reviewer note: this is the same Draft<T> -> *delta.Object placement that Tx.doc's call:1-5 siblings already carry as reviewed entries. It is a strong candidate for approval as a direct extension of approved entries; the lead decides.
