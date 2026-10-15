// SPDX-License-Identifier: MIT

package profiling

import "sync/atomic"

// FileRevisionStats counts the file-revision stats of auth.json and models.json (ai.authFileRevision, one per call) and ModelsFileStats the ones of models.json among them (counted at the models store's calls), so a process's auth.json stats are the difference. ProcessMetrics reports both. An increment is one atomic add, so the counters stay in every build.
var FileRevisionStats, ModelsFileStats atomic.Uint64
