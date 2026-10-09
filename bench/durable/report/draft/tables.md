# Tables for placeholder (placeholder)

Source: results file sha256 `bc5bc35484f12afefd1435084d301896e673343d0f35e12fd6baf685ff945ca5`. warm = every turn after the first of every sample, pooled (n = turns); cold = open + first turn, one per sample (n = samples). p50, p95 and max are net of the floor: the p50 of the empty target's same latency at the same size (Rule A on Miniflare; Rule B, the native build, has no request path and no floor). 95% interval = seeded bootstrap of the net p50 (2,000 rounds).

- Rule A: durable-bench's JS model in a Durable Object, the same model path as pi-durable (the headline)
- Rule B: the native build's own scripted model in the core's harness (a harness floor)

> DRAFT. Placeholder data: every value is synthetic, not a measurement. Do not publish.

## standard (miniflare)

| rule | target | turns | samples | floor warm / cold ms | warm n | warm p50 | warm p95 | warm max | warm p50 95% CI | cold p50 | cold p95 | cold max | DB MB | seed s (runs) |
|---|---|---:|---:|---:|---:|---:|---:|---:|---|---:|---:|---:|---:|---:|
| A | pi-durable main `da866ada` | 50 | 12 | 3.1 / 9.2 | 108 | 65 | 68 | 68 | 64-65 | 117 | 122 | 123 | 0.34 | 1.0 (1) |
| A | pi-durable main `da866ada` | 250 | 12 | 3.1 / 9.2 | 108 | 70 | 74 | 74 | 70-71 | 131 | 138 | 139 | 0.88 | 3.2 (1) |
| A | pi-durable main `da866ada` | 1,000 | 12 | 3.1 / 9.3 | 108 | 89 | 94 | 94 | 88-89 | 181 | 195 | 196 | 2.90 | 11.4 (1) |
| A | pi-durable main `da866ada` | 3,500 | 12 | 3.1 / 9.1 | 108 | 153 | 161 | 162 | 151-154 | 366 | 383 | 386 | 9.65 | 38.9 (1) |
| A | Tardigrade `0.44.0` | 50 | 12 | 3.1 / 9.2 | 108 | 135 | 141 | 141 | 133-136 | 363 | 379 | 382 | 0.80 | 4.8 (1) |
| A | Tardigrade `0.44.0` | 250 | 12 | 3.1 / 9.2 | 108 | 147 | 156 | 157 | 146-148 | 545 | 554 | 556 | 2.38 | 21.8 (1) |
| A | Tardigrade `0.44.0` | 1,000 | 12 | 3.1 / 9.3 | 108 | 203 | 212 | 213 | 202-205 | 1,149 | 1,190 | 1,193 | 8.30 | 85.5 (1) |
| A | Tardigrade `0.44.0` | 3,500 | 12 | 3.1 / 9.1 | 108 | 380 | 402 | 403 | 377-382 | 3,299 | 3,359 | 3,373 | 28.05 | 298.0 (1) |
| A | PiG Durable (TinyGo) `placeholder` | 50 | 12 | 3.1 / 9.2 | 108 | 28 | 29 | 30 | 27-28 | 52 | 54 | 54 | 0.34 | 0.7 (1) |
| A | PiG Durable (TinyGo) `placeholder` | 250 | 12 | 3.1 / 9.2 | 108 | 29 | 30 | 31 | 28-29 | 56 | 58 | 58 | 0.88 | 2.1 (1) |
| A | PiG Durable (TinyGo) `placeholder` | 1,000 | 12 | 3.1 / 9.3 | 108 | 32 | 34 | 34 | 31-32 | 62 | 66 | 66 | 2.90 | 7.3 (1) |
| A | PiG Durable (TinyGo) `placeholder` | 3,500 | 12 | 3.1 / 9.1 | 108 | 42 | 44 | 45 | 41-42 | 88 | 91 | 92 | 9.65 | 24.8 (1) |
| A | PiG Durable (Go) `placeholder` | 50 | 12 | 3.1 / 9.2 | 108 | 33 | 35 | 35 | 33-33 | 105 | 109 | 109 | 0.34 | 0.7 (1) |
| A | PiG Durable (Go) `placeholder` | 250 | 12 | 3.1 / 9.2 | 108 | 34 | 36 | 36 | 34-34 | 110 | 114 | 114 | 0.88 | 2.3 (1) |
| A | PiG Durable (Go) `placeholder` | 1,000 | 12 | 3.1 / 9.3 | 108 | 38 | 40 | 40 | 37-38 | 122 | 130 | 130 | 2.90 | 8.3 (1) |
| A | PiG Durable (Go) `placeholder` | 3,500 | 12 | 3.1 / 9.1 | 108 | 51 | 53 | 54 | 50-51 | 175 | 182 | 183 | 9.65 | 28.3 (1) |
| A | Durable core (TS) `placeholder` | 50 | 12 | 3.1 / 9.2 | 108 | 38 | 40 | 41 | 38-39 | 77 | 78 | 79 | 0.34 | 0.8 (1) |
| A | Durable core (TS) `placeholder` | 250 | 12 | 3.1 / 9.2 | 108 | 40 | 42 | 42 | 40-40 | 82 | 85 | 85 | 0.88 | 2.6 (1) |
| A | Durable core (TS) `placeholder` | 1,000 | 12 | 3.1 / 9.3 | 108 | 46 | 49 | 49 | 45-47 | 102 | 108 | 108 | 2.90 | 9.3 (1) |
| A | Durable core (TS) `placeholder` | 3,500 | 12 | 3.1 / 9.1 | 108 | 67 | 70 | 71 | 66-67 | 180 | 188 | 189 | 9.65 | 31.8 (1) |
| A | Empty target (raw) `floor` | 50 | 12 | 0.0 / 0.0 | 108 | 3 | 3 | 3 | 3-3 | 9 | 10 | 10 | 0.00 | – (0) |
| A | Empty target (raw) `floor` | 250 | 12 | 0.0 / 0.0 | 108 | 3 | 3 | 3 | 3-3 | 9 | 10 | 10 | 0.00 | – (0) |
| A | Empty target (raw) `floor` | 1,000 | 12 | 0.0 / 0.0 | 108 | 3 | 3 | 3 | 3-3 | 9 | 10 | 10 | 0.00 | – (0) |
| A | Empty target (raw) `floor` | 3,500 | 12 | 0.0 / 0.0 | 108 | 3 | 3 | 3 | 3-3 | 9 | 9 | 10 | 0.00 | – (0) |

## compact-big (miniflare)

| rule | target | turns | samples | floor warm / cold ms | warm n | warm p50 | warm p95 | warm max | warm p50 95% CI | cold p50 | cold p95 | cold max | DB MB | seed s (runs) |
|---|---|---:|---:|---:|---:|---:|---:|---:|---|---:|---:|---:|---:|---:|
| A | PiG Durable (TinyGo) `placeholder` | 50 | 12 | 3.1 / 9.2 | 108 | 25 | 26 | 26 | 24-25 | 48 | 49 | 49 | 0.74 | 1.0 (1) |
| A | PiG Durable (TinyGo) `placeholder` | 250 | 12 | 3.1 / 9.2 | 108 | 25 | 27 | 27 | 25-26 | 49 | 51 | 51 | 1.93 | 3.3 (1) |
| A | PiG Durable (TinyGo) `placeholder` | 1,000 | 12 | 3.1 / 9.3 | 108 | 28 | 30 | 30 | 28-29 | 54 | 58 | 59 | 6.38 | 11.7 (1) |
| A | PiG Durable (TinyGo) `placeholder` | 3,500 | 12 | 3.1 / 9.1 | 108 | 37 | 40 | 40 | 37-38 | 79 | 83 | 83 | 21.23 | 39.7 (1) |
| A | pi-durable main `da866ada` | 50 | 12 | 3.1 / 9.2 | 108 | 58 | 61 | 62 | 57-59 | 105 | 109 | 109 | 0.74 | 1.5 (1) |
| A | pi-durable main `da866ada` | 250 | 12 | 3.1 / 9.2 | 108 | 62 | 66 | 66 | 62-63 | 116 | 123 | 124 | 1.93 | 5.0 (1) |
| A | pi-durable main `da866ada` | 1,000 | 12 | 3.1 / 9.3 | 108 | 79 | 84 | 85 | 78-80 | 167 | 173 | 174 | 6.38 | 18.2 (1) |
| A | pi-durable main `da866ada` | 3,500 | 12 | 3.1 / 9.1 | 108 | 139 | 145 | 146 | 137-140 | 329 | 343 | 345 | 21.23 | 62.2 (1) |

## big (miniflare)

| rule | target | turns | samples | floor warm / cold ms | warm n | warm p50 | warm p95 | warm max | warm p50 95% CI | cold p50 | cold p95 | cold max | DB MB | seed s (runs) |
|---|---|---:|---:|---:|---:|---:|---:|---:|---|---:|---:|---:|---:|---:|
| A | PiG Durable (TinyGo) `placeholder` | 50 | 12 | 3.1 / 9.2 | 108 | 43 | 46 | 46 | 43-44 | 82 | 87 | 87 | 0.74 | 1.0 (1) |
| A | PiG Durable (TinyGo) `placeholder` | 250 | 12 | 3.1 / 9.2 | 108 | 45 | 47 | 47 | 44-45 | 86 | 89 | 89 | 1.93 | 3.3 (1) |
| A | PiG Durable (TinyGo) `placeholder` | 1,000 | 12 | 3.1 / 9.3 | 108 | 49 | 52 | 52 | 48-50 | 99 | 103 | 104 | 6.38 | 11.7 (1) |
| A | PiG Durable (TinyGo) `placeholder` | 3,500 | 12 | 3.1 / 9.1 | 108 | 64 | 68 | 68 | 64-65 | 136 | 143 | 143 | 21.23 | 39.7 (1) |
| A | pi-durable main `da866ada` | 50 | 12 | 3.1 / 9.2 | 108 | 99 | 104 | 105 | 98-100 | 179 | 188 | 189 | 0.74 | 1.5 (1) |
| A | pi-durable main `da866ada` | 250 | 12 | 3.1 / 9.2 | 108 | 106 | 112 | 112 | 105-107 | 200 | 211 | 212 | 1.93 | 5.0 (1) |
| A | pi-durable main `da866ada` | 1,000 | 12 | 3.1 / 9.3 | 108 | 136 | 142 | 143 | 133-138 | 283 | 295 | 295 | 6.38 | 18.2 (1) |
| A | pi-durable main `da866ada` | 3,500 | 12 | 3.1 / 9.1 | 108 | 232 | 244 | 245 | 228-235 | 534 | 565 | 574 | 21.23 | 62.2 (1) |

## standard (native)

| rule | target | turns | samples | floor warm / cold ms | warm n | warm p50 | warm p95 | warm max | warm p50 95% CI | cold p50 | cold p95 | cold max | DB MB | seed s (runs) |
|---|---|---:|---:|---:|---:|---:|---:|---:|---|---:|---:|---:|---:|---:|
| B | PiG Durable (native) `placeholder` | 50 | 12 | 0.0 / 0.0 | 108 | 12 | 13 | 13 | 12-13 | 21 | 21 | 21 | 0.34 | 0.4 (1) |
| B | PiG Durable (native) `placeholder` | 250 | 12 | 0.0 / 0.0 | 108 | 13 | 14 | 14 | 13-13 | 21 | 22 | 22 | 0.88 | 1.2 (1) |
| B | PiG Durable (native) `placeholder` | 1,000 | 12 | 0.0 / 0.0 | 108 | 14 | 15 | 15 | 14-14 | 24 | 25 | 26 | 2.90 | 4.2 (1) |
| B | PiG Durable (native) `placeholder` | 3,500 | 12 | 0.0 / 0.0 | 108 | 19 | 20 | 21 | 19-20 | 35 | 36 | 36 | 9.65 | 14.2 (1) |

## standard (cloudflare)

| rule | target | turns | samples | floor warm / cold ms | warm n | warm p50 | warm p95 | warm max | warm p50 95% CI | cold p50 | cold p95 | cold max | DB MB | seed s (runs) |
|---|---|---:|---:|---:|---:|---:|---:|---:|---|---:|---:|---:|---:|---:|
| A | PiG Durable (TinyGo) `placeholder` | 50 | 5 | 0.0 / 0.0 | 45 | 18 | 19 | 20 | 18-19 | 37 | 39 | 39 | 0.34 | 0.7 (1) |
| A | PiG Durable (TinyGo) `placeholder` | 3,500 | 5 | 0.0 / 0.0 | 45 | 27 | 28 | 29 | 27-28 | 59 | 61 | 61 | 9.65 | 24.8 (1) |
| A | pi-durable main `da866ada` | 50 | 5 | 0.0 / 0.0 | 45 | 40 | 42 | 43 | 40-41 | 76 | 79 | 79 | 0.34 | 1.0 (1) |
| A | pi-durable main `da866ada` | 3,500 | 5 | 0.0 / 0.0 | 45 | 94 | 98 | 99 | 92-95 | 219 | 231 | 232 | 9.65 | 38.9 (1) |
| A | Tardigrade `0.44.0` | 50 | 5 | 0.0 / 0.0 | 45 | 82 | 86 | 87 | 80-83 | 217 | 227 | 228 | 0.80 | 4.8 (1) |
| A | Tardigrade `0.44.0` | 3,500 | 5 | 0.0 / 0.0 | 45 | 232 | 242 | 244 | 228-234 | 1,903 | 1,954 | 1,965 | 28.05 | 298.0 (1) |

## Where it runs: per-request probe (Miniflare; RUNNER-SPEC 4a)

Medians over samples of: warm = median of turns 2-10 per sample; cold = open + first turn. Peak MB = largest JS heap + ArrayBuffers + Wasm memory read after any request, before collection, so it counts garbage. Retained MB = the same sum after a full collection after the last turn: what the isolate holds (decimal MB; limit 128 MB). Wasm high-water MB = the largest memory.buffer.byteLength read after any request (Wasm memory never shrinks). All Rule A. Core ms from "time" runs only.

| target | turns | n count | n time | CPU warm ms | CPU cold ms | instantiate ms | rows read/turn | rows written/turn | peak MB (before GC) | retained MB | Wasm high-water MB | crossings/turn | hop steps/turn | core ms/turn |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| pi-durable main `da866ada` | 50 | 3 | 3 | 64.8 | 117.5 | 0.0 | 1,202 | 869 | 10.9 | 5.7 | 0.0 | 0 | 0 | 0.0 |
| pi-durable main `da866ada` | 250 | 3 | 3 | 69.3 | 135.0 | 0.0 | 1,208 | 869 | 11.6 | 6.1 | 0.0 | 0 | 0 | 0.0 |
| pi-durable main `da866ada` | 1,000 | 3 | 3 | 87.5 | 180.8 | 0.0 | 1,230 | 869 | 13.9 | 7.4 | 0.0 | 0 | 0 | 0.0 |
| pi-durable main `da866ada` | 3,500 | 3 | 3 | 147.8 | 367.6 | 0.0 | 1,305 | 869 | 21.7 | 11.9 | 0.0 | 0 | 0 | 0.0 |
| PiG Durable (TinyGo) `placeholder` | 50 | 3 | 3 | 29.1 | 58.9 | 3.0 | 500 | 869 | 23.3 | 15.3 | 6.1 | 1,308 | 310 | 15.8 |
| PiG Durable (TinyGo) `placeholder` | 250 | 3 | 3 | 29.9 | 60.9 | 3.0 | 500 | 869 | 24.0 | 15.8 | 6.4 | 1,310 | 310 | 15.9 |
| PiG Durable (TinyGo) `placeholder` | 1,000 | 3 | 3 | 33.0 | 69.5 | 3.0 | 500 | 869 | 26.4 | 17.6 | 7.5 | 1,305 | 310 | 17.8 |
| PiG Durable (TinyGo) `placeholder` | 3,500 | 3 | 3 | 43.0 | 89.6 | 3.0 | 500 | 869 | 34.1 | 23.6 | 11.3 | 1,307 | 310 | 22.9 |
| PiG Durable (Go) `placeholder` | 50 | 3 | 3 | 33.8 | 107.3 | 20.0 | 500 | 869 | 29.4 | 21.4 | 12.1 | 1,304 | 310 | 19.8 |
| PiG Durable (Go) `placeholder` | 250 | 3 | 3 | 35.4 | 108.3 | 20.0 | 500 | 869 | 30.1 | 22.1 | 12.6 | 1,311 | 310 | 20.8 |
| PiG Durable (Go) `placeholder` | 1,000 | 3 | 3 | 39.0 | 131.0 | 20.0 | 500 | 869 | 33.2 | 24.6 | 14.5 | 1,305 | 310 | 22.2 |
| PiG Durable (Go) `placeholder` | 3,500 | 3 | 3 | 50.7 | 176.0 | 20.0 | 500 | 869 | 43.6 | 33.1 | 20.8 | 1,309 | 310 | 29.3 |

## Where it runs: per-request probe (Cloudflare; bench/cloud.ts)

Count runs only. CPU ms = Cloudflare's CPU time of the Worker invocation plus the Durable Object invocations it caused (wrangler tail). The JS heap is not observable there: peak MB = Wasm memory only.

| target | turns | n | CPU warm ms | CPU cold ms | rows read/turn | rows written/turn | Wasm peak MB | crossings/turn | hop steps/turn |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| pi-durable main `da866ada` | 50 | 3 | 45.3 | 87.6 | 1,202 | 869 | 0.0 | 0 | 0 |
| pi-durable main `da866ada` | 3,500 | 3 | 102.9 | 251.2 | 1,305 | 869 | 0.0 | 0 | 0 |
| PiG Durable (TinyGo) `placeholder` | 50 | 3 | 20.5 | 42.4 | 500 | 869 | 6.1 | 1,306 | 310 |
| PiG Durable (TinyGo) `placeholder` | 3,500 | 3 | 29.6 | 66.8 | 500 | 869 | 11.3 | 1,310 | 310 |

## Upload size (bench/size.ts)

| target | bytes | of which Wasm | gzip (reference) |
|---|---:|---:|---:|
| pi-durable main | 900,000 | 0 | 243,000 |
| PiG Durable (TinyGo) | 1,700,000 | 750,000 | 459,000 |
| PiG Durable (Go) | 6,300,000 | 5,300,000 | 1,701,000 |
