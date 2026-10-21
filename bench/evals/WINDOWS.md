<!--
SPDX-License-Identifier: MIT
-->

# Running the eval suite on Windows 11 (native) and in WSL

Native Windows x64 runs the recipe below: the unit suite, `lock verify`, `arms`, `startup` and `overhead` (`version`, `print`, `rpc`) pass in PowerShell and inside a ConPTY console, with Python 3.11 and 3.12. The Job Object meter is unit-tested against a fake Win32 API on every host and against the real one on Windows. WSL2 is a Linux run: use the Linux recipe in `README.md` and record the kernel (`uname -r`) with the result.

## Native Windows (PowerShell)

Prerequisites: Python 3.12 or newer, Go 1.27.3, Node 24.19, git. High performance power plan, AC power, nothing else running, real-time antivirus scanning excluded for the throwaway directory (`%TEMP%`), no vendor login and no API key in the environment.

```powershell
git clone <repo> pig; cd pig; git checkout <commit>
cd bench\evals
python -m unittest discover -s tests -t .          # first thing to report: failures here
python -m pigbench lock verify                      # offline
python -m pigbench arms --arms pig-0.4.1 rpi        # release zips, checksum and --version checked
python -m pigbench startup  --arms pig-0.4.1 rpi --rounds 4 --runs 5 --out ..\..\tmp\bench\startup-win
python -m pigbench overhead --arms pig-0.4.1 rpi --scenarios version print rpc --runs 20 --warmup 2 --out ..\..\tmp\bench\overhead-win
```

Expected limits: Pi 1.1.0, Codex, Copilot and a built `pig-0.5` are not yet runnable on Windows; the PTY scenario, the egress audit and thread counts are not implemented; `taskset`, cgroups and `/proc` are not used. The overhead report states the meter mode (`windows-job`), that peak memory is the job's committed memory, and that PSS is replaced by the tree's private bytes (commit charge, `PrivateUsage`).

hyperfine 2.0 has no memory metric on Windows, so the startup report has wall and CPU time only; the overhead `version` scenario meters the same command's peak memory. Windows charges process CPU time in whole clock ticks (15.625 ms by default), so every CPU value is a multiple of the tick: both reports state the tick and give the pooled mean of each CPU metric next to the medians. In the environment block, `cpu_governor` is the active power plan's display name and the power source, for example `High performance plan, AC power`. The recipe asks for the High performance plan on AC power; a Modern Standby device offers only the Balanced plan. The cross-architecture report states these meter and CPU differences for each Windows machine and does not rank a CPU median under 10 ticks.

Copy each `tmp\bench\*-win\raw.json` to the machine that aggregates and run `python3 -m pigbench aggregate <dir>...` there. Runs must share one commit and one `arms.lock`.
