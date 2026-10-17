#!/bin/sh
# Scale runs (robust-durable item 3): durable-bench's run and probe for each target at each size, once, on one CPU.
#   DB=<durable-bench checkout> PIG_SOURCE=<PiG worktree> PIG_VERSION=<sha> [TARGETS="pig-tinygo pi-head"] [SIZES="3500 10000 25000"] [CPU=11] [OUT=dir] sh scale.sh
# The 10,000- and 25,000-turn fixtures come from durable/contract/tools/extend-fixture.mjs applied to durable-bench's 3,500.
set -u
cd "$DB"
export PI_SOURCE=${PI_SOURCE:-../pi} PI_HEAD_VERSION=${PI_HEAD_VERSION:-da866ada} CPU_MAX=1 SAMPLES=1
SIZES=${SIZES:-"3500 10000 25000"}
OUT=${OUT:-$DB/results/scale}
mkdir -p "$OUT"
for t in ${TARGETS:-pig-tinygo pi-head}; do
	echo "== run $t $SIZES $(date +%T)"
	taskset -c "${CPU:-11}" node bench/run.ts "$t" $SIZES 2>&1 | grep -v "^cpu " | tee -a "$OUT/run.log"
	for mode in time count; do
		echo "== probe $mode $t $(date +%T)"
		BENCH_PROBE=$mode taskset -c "${CPU:-11}" node bench/probe.ts "$t" $SIZES 2>&1 | tee -a "$OUT/run.log"
	done
done
mv results/results.jsonl results/probe.jsonl "$OUT/" 2>/dev/null
echo "EXIT $(date +%T)" | tee -a "$OUT/run.log"
