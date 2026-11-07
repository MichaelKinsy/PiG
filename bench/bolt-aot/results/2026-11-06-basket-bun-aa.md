# Bun A/A floor for the digest-cache deltas (2026-11-06) (PROVISIONAL: do not publish)

Why: the review of `2026-11-06-basket-after.md` noted that Bun had no A/A arm, so its before/after deltas (Go host CPU to ready 280 to 190 ms, launch to ready 1052 to 960 ms on `five` and 506 to 406 ms on `ten`) had no measured noise floor. This is a reduced session with `bun2` and `bun-a2` repeat arms and `node2`, `node-a2` kept.

## Method

- One interleaved session, 12 rounds (arm order rotated each round, one recorded warm-up run per arm and set before them), sets `five` and `ten`, 20 turns, same metrics as the BEFORE/AFTER files. No aot arm, so no image build and no cold pass (`--alloc-rounds 0 --cold-rounds 0`).
- Eight arms: `node`, `node2`, `bun`, `bun2` on the Go host of **`741f27c59b`** (BEFORE; slot `slot-b`), and `node-a`, `node-a2`, `bun-a`, `bun-a2` on the Go host of **`af9566210d`** (AFTER, branch `fix/071-pigaot-r2base`; slot `slot-a`). Both Go hosts built in the CI container with `-trimpath`; their sizes (93,080,560 and 93,101,727 bytes) equal those of the 741f27c59b and 5f6145dc3 builds of the earlier sessions. Stock Bun 1.4.2 (SHA-256 `a83d2637...` = the lock's) in each slot, Node 26.12.1 (`--node current`). Every Bun run is checked against `<slot>/bun/bun` by `/proc/<pid>/exe`; the AFTER arms had the digest record beside Bun after ready in every run (1 of 1 in every cell), the BEFORE arms never (0).
- Harness: `basket.py` at `6af2aa616` (this branch: `--bun` now installs Bun into the slots without `--build`, which builds images), and the new `basket_aa.py`, which turns the JSONL into the paired A/A floor and the era delta with a bootstrap interval and a Mann-Whitney test.
- Host (xd670, container capped at 16 CPUs, Xeon Platinum 8468, kernel 7.0.0-38): 1-minute load average **before 48, during the runs median 50 (min 33, max 71), after 64** (the earlier sessions ran at median 25), so absolute times are slower than in the earlier files; only the interleaved comparisons mean anything.

## Tables (selected rows of the generated report; full report kept on xd670 only, not committed)

Cells median [p10-p90] over 12 runs; the A/A columns are |median(a) - median(b)| / their mean for each arm and its repeat.

### Set `five`

| Metric | node | node2 | bun | bun2 | node-a | node-a2 | bun-a | bun-a2 | A/A node-node2 (%) | A/A bun-bun2 (%) | A/A node-a-node-a2 (%) | A/A bun-a-bun-a2 (%) |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Runs ok / total | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | | | | |
| Launch to ready (ms) | 1135 [875-1185] | 1128 [900-1200] | 935 [815-1055] | 1020 [969-1073] | 1124 [871-1215] | 1163 [906-1205] | 853 [746-973] | 899 [784-963] | 1 | 9 | 3 | 5 |
| Go host CPU to ready, user+sys (ms) | 170 [150-190] | 180 [140-200] | 260 [203-280] | 270 [201-289] | 175 [141-190] | 180 [153-190] | 175 [140-190] | 180 [152-190] | 6 | 4 | 3 | 3 |
| Extension host CPU to ready, user+sys (ms) | 1275 [991-1319] | 1260 [984-1363] | 970 [910-1139] | 1110 [1060-1139] | 1280 [1004-1349] | 1280 [973-1339] | 1010 [920-1147] | 1065 [912-1128] | 1 | 13 | 0 | 5 |
| Digest record present beside the executable after ready (1 = yes; Node: n/a) | n/a | n/a | 0 [0-0] | 0 [0-0] | n/a | n/a | 1 [1-1] | 1 [1-1] | - | - | - | 0 |
| Turn 1 wall (ms) | 39 [32-42] | 41 [37-47] | 40 [38-44] | 39 [36-45] | 40 [36-44] | 41 [33-45] | 40 [35-42] | 41 [34-44] | 6 | 3 | 3 | 3 |
| Steady wall, median of turns 6-20 (ms) | 13.3 [12.3-14.6] | 12.9 [11.4-14.2] | 12.2 [10.6-13.8] | 11.6 [9.7-13.0] | 13.3 [10.8-15.3] | 13.3 [11.7-14.7] | 11.9 [10.9-13.5] | 11.9 [9.6-12.8] | 2 | 5 | 0 | 0 |
| Go host CPU per turn, turns 2-20 (ms) | 10.8 [9.5-11.1] | 10.8 [9.5-11.5] | 10.8 [9.5-12.1] | 10.0 [8.4-11.5] | 10.5 [8.5-12.6] | 11.1 [9.5-11.6] | 10.8 [9.5-12.1] | 10.8 [9.0-12.1] | 0 | 8 | 5 | 0 |
| Extension host CPU per turn, turns 2-20 (ms) | 8.9 [7.4-9.5] | 8.2 [7.4-10.5] | 6.8 [6.3-8.8] | 7.1 [6.8-7.9] | 8.4 [7.9-9.9] | 8.7 [7.4-9.5] | 6.8 [6.3-7.9] | 6.8 [6.3-7.9] | 9 | 4 | 3 | 0 |
| Extension host RSS @ready+0.5s | 167.2 [166.4-167.9] | 167.5 [167.0-168.1] | 162.2 [160.2-164.3] | 162.2 [160.5-164.2] | 167.2 [166.5-168.1] | 167.1 [166.3-168.2] | 162.6 [161.5-164.5] | 162.0 [160.8-164.1] | 0 | 0 | 0 | 0 |
| Extension host PSS @ready+0.5s | 161.9 [160.9-163.7] | 162.3 [161.5-162.9] | 160.1 [158.0-162.1] | 160.1 [158.3-161.9] | 161.6 [161.1-163.2] | 161.6 [160.9-163.1] | 160.5 [159.3-162.3] | 160.0 [158.7-162.0] | 0 | 0 | 0 | 0 |
| Extension host RSS @after 20 turns | 182.3 [179.2-183.2] | 182.1 [179.8-182.5] | 165.0 [163.2-167.5] | 164.0 [161.2-166.4] | 182.3 [178.9-183.2] | 179.5 [179.1-182.1] | 164.7 [160.9-168.3] | 165.7 [163.3-166.7] | 0 | 1 | 2 | 1 |
| Extension host PSS @after 20 turns | 176.9 [173.9-179.1] | 177.1 [174.3-177.3] | 162.8 [161.1-165.2] | 161.8 [158.9-164.2] | 176.8 [173.5-178.0] | 174.3 [173.5-177.1] | 162.5 [158.7-166.1] | 163.5 [161.2-164.5] | 0 | 1 | 1 | 1 |

### Set `ten`

| Metric | node | node2 | bun | bun2 | node-a | node-a2 | bun-a | bun-a2 | A/A node-node2 (%) | A/A bun-bun2 (%) | A/A node-a-node-a2 (%) | A/A bun-a-bun-a2 (%) |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Runs ok / total | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | 12/12 | | | | |
| Launch to ready (ms) | 376 [345-418] | 378 [329-429] | 422 [380-520] | 427 [393-497] | 388 [339-440] | 379 [336-433] | 352 [308-363] | 399 [342-421] | 1 | 1 | 2 | 12 |
| Go host CPU to ready, user+sys (ms) | 155 [150-189] | 160 [140-179] | 240 [211-289] | 240 [211-296] | 150 [141-200] | 165 [150-190] | 155 [140-189] | 195 [145-200] | 3 | 0 | 10 | 23 |
| Extension host CPU to ready, user+sys (ms) | 320 [281-350] | 290 [280-399] | 260 [241-337] | 285 [251-329] | 305 [280-380] | 320 [281-359] | 270 [250-318] | 295 [261-329] | 10 | 9 | 5 | 9 |
| Digest record present beside the executable after ready (1 = yes; Node: n/a) | n/a | n/a | 0 [0-0] | 0 [0-0] | n/a | n/a | 1 [1-1] | 1 [1-1] | - | - | - | 0 |
| Turn 1 wall (ms) | 42 [39-48] | 42 [38-49] | 44 [36-49] | 42 [31-51] | 47 [38-55] | 44 [38-52] | 40 [35-48] | 41 [36-44] | 1 | 3 | 8 | 1 |
| Steady wall, median of turns 6-20 (ms) | 38.5 [35.7-44.4] | 39.9 [34.4-42.0] | 30.5 [29.0-34.7] | 33.2 [28.7-37.6] | 39.1 [35.9-44.7] | 39.7 [34.2-43.2] | 32.3 [27.6-34.2] | 31.7 [28.2-38.1] | 4 | 9 | 1 | 2 |
| Go host CPU per turn, turns 2-20 (ms) | 17.9 [15.4-19.4] | 16.8 [15.8-19.5] | 16.3 [15.3-18.9] | 17.4 [15.8-19.4] | 17.4 [16.4-19.9] | 16.8 [15.8-20.8] | 17.4 [15.4-18.9] | 17.4 [15.3-19.4] | 6 | 6 | 3 | 0 |
| Extension host CPU per turn, turns 2-20 (ms) | 39.7 [35.9-45.3] | 40.5 [36.1-42.6] | 32.9 [31.6-36.3] | 34.2 [28.8-37.4] | 41.6 [36.9-46.2] | 41.3 [35.6-47.2] | 33.7 [30.1-37.7] | 33.7 [28.8-37.7] | 2 | 4 | 1 | 0 |
| Extension host RSS @ready+0.5s | 109.9 [108.3-111.8] | 111.1 [108.6-113.3] | 96.5 [96.1-98.2] | 97.5 [96.4-98.3] | 111.4 [110.1-113.3] | 111.7 [108.8-113.0] | 97.5 [96.3-98.6] | 97.6 [96.2-98.4] | 1 | 1 | 0 | 0 |
| Extension host PSS @ready+0.5s | 104.5 [102.6-106.1] | 105.6 [102.9-108.1] | 94.2 [93.8-95.9] | 95.3 [94.2-95.9] | 105.9 [104.9-107.8] | 106.2 [103.0-107.5] | 95.2 [94.0-96.2] | 95.3 [93.9-96.1] | 1 | 1 | 0 | 0 |
| Extension host RSS @after 20 turns | 196.7 [193.1-200.2] | 197.4 [189.4-201.0] | 143.4 [138.0-150.3] | 143.2 [139.1-149.1] | 196.9 [187.5-202.2] | 194.4 [185.2-199.5] | 150.0 [139.8-162.5] | 147.3 [144.8-159.2] | 0 | 0 | 1 | 2 |
| Extension host PSS @after 20 turns | 191.1 [187.4-194.8] | 191.9 [183.8-195.5] | 141.2 [135.7-148.0] | 140.9 [136.8-146.8] | 191.5 [182.0-196.5] | 188.8 [179.4-193.7] | 147.7 [137.6-160.1] | 145.1 [142.5-156.9] | 0 | 0 | 1 | 2 |


## The A/A floor, paired per round (`basket_aa.py`)

Each arm and its repeat ran in the same round; the table shows the per-round difference (ms): median, median absolute, largest absolute.

| set | metric | bun - bun2 (BEFORE) | bun-a - bun-a2 (AFTER) | node - node2 | node-a - node-a2 |
|---|---|---|---|---|---|
| five | Go host CPU to ready | -5, 25, 120 | 0, 15, 50 | -5, 25, 60 | 0, 10, 40 |
| five | launch to ready | -79, 96, 328 | -20, 58, 211 | -28, 130, 302 | -12, 42, 108 |
| ten | Go host CPU to ready | +5, 20, 50 | -30, 30, 60 | +5, 20, 60 | 0, 5, 40 |
| ten | launch to ready | +10, 21, 80 | -42, 42, 115 | -1, 18, 88 | -2, 34, 119 |

## The Bun deltas against that floor

Pooled arm pairs (bun+bun2, n = 24, against bun-a+bun-a2, n = 24); "per-round delta" is the median over the 12 rounds of (mean of the two AFTER repeats) minus (mean of the two BEFORE repeats) with a 95% bootstrap interval over rounds; p is a two-sided Mann-Whitney test of the pooled samples. The Node pairs (the same Go-host change cannot touch Node's path) are the control.

| set | metric | Bun before -> after (median) | per-round delta, 95% CI | p | Node control: before -> after, delta [CI], p |
|---|---|---|---|---|---|
| five | Go host CPU to ready | 260 -> 180 ms | -80 ms [-92, -65] | 4.7e-9 | 180 -> 180; -5 [-5, +10]; 0.97 |
| five | launch to ready | 1011 -> 899 ms | -116 ms [-140, -81] | 2.4e-4 | 1129 -> 1149; +71 [-21, +135]; 0.28 |
| five | extension host CPU to ready | 1105 -> 1060 ms | -10 ms [-72, +5] | 0.32 | 1270 -> 1280; +65 [-38, +132]; 0.48 |
| five | turn 1 / steady turn wall | 40 -> 40 / 12 -> 12 ms | 0 [-3, +1] / 0 [0, +1] | 0.65 / 0.95 | 41 -> 40 / 13 -> 13; p 0.73 / 0.76 |
| ten | Go host CPU to ready | 240 -> 185 ms | -75 ms [-95, -45] | 6.6e-9 | 160 -> 150; +5 [-8, +15]; 0.68 |
| ten | launch to ready | 424 -> 361 ms | -74 ms [-84, -43] | 2.6e-6 | 378 -> 386; +6 [-34, +38]; 0.71 |
| ten | extension host CPU to ready | 275 -> 280 ms | +2 ms [-18, +18] | 0.76 | 310 -> 315; -5 [-35, +35]; 0.63 |
| ten | turn 1 / steady turn wall | 43 -> 41 / 32 -> 32 ms | -3 [-7, +6] / 0 [-2, +2] | 0.19 / 0.98 | 42 -> 44 / 39 -> 40; p 0.25 / 0.71 |

## Verdict

- **Go host CPU to ready (-80 ms on `five`, -75 ms on `ten`; 260 -> 180 and 240 -> 185 ms pooled; 280 -> 190 and 290 -> 195 ms in the earlier file) exceeds the Bun floor by a wide margin.** The paired A/A differences are at most 120 ms and typically 15-30 ms (median absolute); the delta's bootstrap interval excludes zero by 45-65 ms and the test gives p < 1e-8; the Node control does not move (-5 and +5 ms, p 0.97 and 0.68). The p10-p90 ranges of the two eras do not overlap in either set (BEFORE [201, 296], AFTER [140, 200]).
- **Launch to ready (-116 ms on `five`, -74 ms on `ten`; -11% and -17%) also exceeds the floor, but only just in the sense that the table's own A/A column cannot show it:** one `bun` against `bun2` pair differs by 9% (five) and the AFTER pair by 12% (ten), as large as the table's before/after percentage, and a single round of one pair differs by up to 328 ms. Pooled over both repeats and paired by round the delta is clear (intervals [-140, -81] and [-84, -43] ms, p 2.4e-4 and 2.6e-6, Node control not significant: +71 [-21, +135] and +6 [-34, +38]). Its size, 74-116 ms, matches the ~80 ms of Go host CPU removed (the hash of the 79.5 MB binary) and the first session's -92 and -100 ms; I read it as the cache's effect, with the caveat that its percentage (-11% / -17%) is of the same order as the floor of a single arm pair, so quote it as "about 70-120 ms sooner", not as a precise percentage.
- Nothing else about Bun moved: extension host CPU to ready, turn 1, steady turn, per-turn CPU and RSS/PSS agree within the A/A floor, with one exception to watch: `ten` after 20 turns, Bun RSS 143.4 / 143.2 MB (BEFORE pair) against 150.0 / 147.3 (AFTER pair, p90 up to 162), +3% to +5%; the earlier session measured 147.2 against 146.0, and the AFTER pair differ from each other by 2%, so I do not read it as an effect (every other Bun RSS/PSS row differs by 0-1% between eras).
- Caveats: load was twice that of the earlier sessions (median 50), 12 rounds, one session on a shared host; the per-round bootstrap uses 12 values and the Mann-Whitney normal approximation uses 24 against 24.
