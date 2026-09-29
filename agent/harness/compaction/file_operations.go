package compaction

// Ports packages/agent/src/harness/compaction/utils.ts (Set insertion order).

import "slices"

func addFilePath(set *map[string]struct{}, order *[]string, path string) {
	if *set == nil {
		*set = make(map[string]struct{})
	}
	if _, exists := (*set)[path]; exists {
		return
	}
	(*set)[path] = struct{}{}
	*order = append(*order, path)
}

// AddRead records a read path in first-observation order.
func (ops *FileOperations) AddRead(path string) { addFilePath(&ops.Read, &ops.readOrder, path) }

// AddWritten records a written path in first-observation order.
func (ops *FileOperations) AddWritten(path string) {
	addFilePath(&ops.Written, &ops.writtenOrder, path)
}

// AddEdited records an edited path in first-observation order.
func (ops *FileOperations) AddEdited(path string) { addFilePath(&ops.Edited, &ops.editedOrder, path) }

func orderedFilePaths(set map[string]struct{}, order []string) []string {
	result := make([]string, 0, len(set))
	seen := make(map[string]bool, len(order))
	for _, path := range order {
		if _, exists := set[path]; exists && !seen[path] {
			result = append(result, path)
			seen[path] = true
		}
	}
	// Direct Go map insertions have no insertion order; keep their serialization deterministic.
	var remaining []string
	for path := range set {
		if !seen[path] {
			remaining = append(remaining, path)
		}
	}
	slices.Sort(remaining)
	return append(result, remaining...)
}

// Paths returns the three insertion-ordered sets as independent slices.
func (ops FileOperations) Paths() (read, written, edited []string) {
	return orderedFilePaths(ops.Read, ops.readOrder), orderedFilePaths(ops.Written, ops.writtenOrder), orderedFilePaths(ops.Edited, ops.editedOrder)
}
