# Follow-ups

## 0.7.1: the chord Node host sometimes outlives its closed link on Windows

`TestNodeTheFirstUseOfTheLinkAttachesItAndStartsTheHostsHeartbeat` (plugins_node_test.go) failed on Windows native-4
(job 127275539424): "node subprocess exited: exit status 1: its link closed and it did not exit within 5s". The Go side's
`channelEnded` (plugins_node.go) kills the process after the 5 s stop grace when the host ended its link and the process did
not exit; exit status 1 is Windows' status for the kill, not a failure of the host. The cause is not found.

Tried: the host's `FramedPort.disconnect` only `end()`s the socket and leaves the process to exit when the loop empties, which
waits for the Go side's close to reach the socket (on Windows AF_UNIX a pending read can miss a peer close). Destroying the
socket once its end is written (branch `fix/r7-chord-win2-node`, with a Linux test that holds a descriptor of the Go side's
connection open and fails on the host as it is) did not stop the failure on Windows: 4 of 12 jobs of a count-50 loop under file
and CPU churn (run 42041963383) still failed with it. A diagnostic of the host's active resources after the channel ends
(`fix/r7-win2-diag`) did not reproduce in 12 jobs of count 20, so what keeps the process alive is unobserved. Next: reproduce
with that diagnostic under the whole-package mix that reproduced it (the failures came after the rollout and watcher cases), and
read the `DIAG` lines from the failing job's log.
