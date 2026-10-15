# SPDX-License-Identifier: MIT
"""Statistics for paired benchmark comparisons. Pure standard library, seeded, deterministic.

Pairing is by task. Resampling is by task, never by trial: trials of one task share their task's
difficulty, so treating them as independent would overstate precision.
"""
import math
import random
import statistics
from statistics import NormalDist


def median(values):
    return statistics.median(values)


def percentile(values, q):
    """Linear-interpolated percentile, q in [0, 100]."""
    xs = sorted(values)
    if not xs:
        return float("nan")
    if len(xs) == 1:
        return xs[0]
    pos = (len(xs) - 1) * q / 100
    lo = math.floor(pos)
    hi = min(lo + 1, len(xs) - 1)
    return xs[lo] + (xs[hi] - xs[lo]) * (pos - lo)


def summarize(values):
    """n, median, p90, min, max, mean for one metric across runs."""
    if not values:
        return {"n": 0}
    return {"n": len(values), "median": median(values), "p90": percentile(values, 90), "min": min(values), "max": max(values), "mean": statistics.fmean(values)}


def bootstrap_ci(values, stat=median, level=0.95, resamples=2000, seed=0):
    """Percentile bootstrap interval of `stat` over i.i.d. values."""
    if len(values) < 2:
        return (float("nan"), float("nan"))
    rng = random.Random(seed)
    n = len(values)
    stats = sorted(stat([values[rng.randrange(n)] for _ in range(n)]) for _ in range(resamples))
    a = (1 - level) / 2
    return (stats[int(a * (resamples - 1))], stats[int(math.ceil((1 - a) * (resamples - 1)))])


def task_means(trials):
    """{task: [trial values]} -> {task: mean}."""
    return {t: statistics.fmean(v) for t, v in trials.items() if v}


def paired_diffs(a, b):
    """Per-task differences a - b over the tasks both arms have, as (tasks, diffs). a and b map task -> mean value."""
    tasks = sorted(set(a) & set(b))
    return tasks, [a[t] - b[t] for t in tasks]


def paired_bootstrap(diffs, level=0.95, resamples=10000, seed=0):
    """Mean paired difference with a task-level percentile bootstrap interval, and the bootstrap means."""
    n = len(diffs)
    if n == 0:
        return {"n": 0}
    rng = random.Random(seed)
    means = sorted(sum(diffs[rng.randrange(n)] for _ in range(n)) / n for _ in range(resamples))
    a = (1 - level) / 2
    return {"n": n, "mean": sum(diffs) / n, "ci": (means[int(a * (resamples - 1))], means[int(math.ceil((1 - a) * (resamples - 1)))]), "level": level, "_means": means}


def sign_flip_p(diffs, resamples=20000, seed=0):
    """Two-sided paired sign-flip (randomisation) test of mean difference = 0. Exact for n <= 16."""
    n = len(diffs)
    if n == 0:
        return float("nan")
    observed = abs(sum(diffs))
    if n <= 16:
        hits = 0
        for mask in range(1 << n):
            total = sum(d if mask >> i & 1 else -d for i, d in enumerate(diffs))
            hits += abs(total) >= observed - 1e-12
        return hits / (1 << n)
    rng = random.Random(seed)
    hits = 0
    for _ in range(resamples):
        total = sum(d if rng.random() < 0.5 else -d for d in diffs)
        hits += abs(total) >= observed - 1e-12
    return (hits + 1) / (resamples + 1)


def tost(diffs, margin, alpha=0.05, resamples=10000, seed=0):
    """Two one-sided tests of equivalence within +/- margin, by task-level paired bootstrap.

    Equivalence is declared when the (1 - 2 alpha) interval of the mean difference lies inside (-margin, margin),
    which is the TOST decision at level alpha; p is the larger of the two one-sided bootstrap p-values.
    """
    boot = paired_bootstrap(diffs, level=1 - 2 * alpha, resamples=resamples, seed=seed)
    if not boot["n"]:
        return {"n": 0, "equivalent": False, "margin": margin}
    means = boot.pop("_means")
    p_low = sum(m <= -margin for m in means) / len(means)  # H0: true difference <= -margin
    p_high = sum(m >= margin for m in means) / len(means)  # H0: true difference >= +margin
    lo, hi = boot["ci"]
    return {"n": boot["n"], "mean": boot["mean"], "ci": boot["ci"], "level": boot["level"], "margin": margin, "p": max(p_low, p_high), "equivalent": lo > -margin and hi < margin}


def mcnemar_exact(b, c):
    """Exact two-sided McNemar p from discordant counts b (A pass, B fail) and c (A fail, B pass)."""
    n = b + c
    if n == 0:
        return 1.0
    k = min(b, c)
    p = sum(math.comb(n, i) for i in range(k + 1)) / 2 ** n * 2
    return min(1.0, p)


def wilson(successes, n, level=0.95):
    if n == 0:
        return (float("nan"), float("nan"))
    z = NormalDist().inv_cdf(1 - (1 - level) / 2)
    p = successes / n
    denom = 1 + z * z / n
    centre = (p + z * z / (2 * n)) / denom
    half = z * math.sqrt(p * (1 - p) / n + z * z / (4 * n * n)) / denom
    return (centre - half, centre + half)


def ratio_of_totals(a, b, level=0.95, resamples=10000, seed=0):
    """sum(a)/sum(b) over paired tasks, with a task-level bootstrap interval, plus the geometric mean of per-task ratios.

    a and b map task -> value (cost or time per task, positive). The ratio of totals is the number a bill or a wall clock shows;
    the geometric mean weights each task equally. Report both.
    """
    tasks = sorted(set(a) & set(b))
    n = len(tasks)
    if n == 0:
        return {"n": 0}
    xs, ys = [a[t] for t in tasks], [b[t] for t in tasks]
    rng = random.Random(seed)
    ratios = []
    for _ in range(resamples):
        idx = [rng.randrange(n) for _ in range(n)]
        den = sum(ys[i] for i in idx)
        ratios.append(sum(xs[i] for i in idx) / den if den else float("nan"))
    ratios = sorted(r for r in ratios if r == r)
    out = {"n": n, "ratio_of_totals": sum(xs) / sum(ys) if sum(ys) else float("nan")}
    if ratios:
        al = (1 - level) / 2
        out["ci"] = (ratios[int(al * (len(ratios) - 1))], ratios[int(math.ceil((1 - al) * (len(ratios) - 1)))])
    pos = [(x, y) for x, y in zip(xs, ys) if x > 0 and y > 0]
    if pos:
        out["geomean_ratio"] = math.exp(statistics.fmean(math.log(x / y) for x, y in pos))
        out["geomean_n"] = len(pos)
    return out


def holm(pvalues):
    """Holm-Bonferroni adjusted p-values, returned in the order given."""
    order = sorted(range(len(pvalues)), key=lambda i: pvalues[i])
    adjusted = [0.0] * len(pvalues)
    running = 0.0
    for rank, i in enumerate(order):
        running = max(running, min(1.0, (len(pvalues) - rank) * pvalues[i]))
        adjusted[i] = running
    return adjusted


def mde_paired(sd_diff, n, alpha=0.05, power=0.8):
    """Smallest paired mean difference detectable with the given power, for a difference standard deviation and n tasks."""
    z = NormalDist().inv_cdf
    return (z(1 - alpha / 2) + z(power)) * sd_diff / math.sqrt(n)


def noise_floor(diffs, level=0.95, seed=0):
    """What a null comparison (the same harness against itself, A/A) looks like: mean difference and its interval."""
    boot = paired_bootstrap(diffs, level=level, seed=seed)
    boot.pop("_means", None)
    if boot["n"] > 1:
        boot["sd"] = statistics.stdev(diffs)
        boot["mde"] = mde_paired(boot["sd"], boot["n"])
    return boot


def ratio_of_medians(a, b, level=0.95, resamples=4000, seed=0):
    """median(a) / median(b) with a percentile-bootstrap interval; the two samples are resampled independently. Needs at least two values in each.

    A resample whose b median is 0 has an infinite ratio when its a median is positive, so a reference that is often 0 widens the interval
    to infinity instead of dropping those resamples (which would narrow it). A resample where both medians are 0 has no ratio and is left out.
    The interval uses the same percentile indices as bootstrap_ci."""
    rng = random.Random(seed)

    def ratio(x, y):
        if y:
            return x / y
        return math.inf if x else None

    point = ratio(statistics.median(a), statistics.median(b))
    ratios = []
    for _ in range(resamples):
        r = ratio(statistics.median(rng.choices(a, k=len(a))), statistics.median(rng.choices(b, k=len(b))))
        if r is not None:
            ratios.append(r)
    if not ratios:
        return {"ratio": point, "ci": [math.nan, math.nan], "n": [len(a), len(b)]}
    ratios.sort()
    alpha = (1 - level) / 2
    lo = ratios[int(alpha * (len(ratios) - 1))]
    hi = ratios[int(math.ceil((1 - alpha) * (len(ratios) - 1)))]
    return {"ratio": point, "ci": [lo, hi], "n": [len(a), len(b)]}
