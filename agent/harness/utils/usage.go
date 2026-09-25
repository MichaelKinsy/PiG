// Package utils holds the harness usage arithmetic, adaptive publication, and
// bounded shell-output capture helpers.
package utils

import "github.com/MichaelKinsy/PiG/ai"

// EmptyUsage returns a zero usage record.
func EmptyUsage() ai.Usage {
	return ai.Usage{}
}

// AddUsage sums two usage records. CacheWrite1h and Reasoning stay absent
// when both inputs omit them.
func AddUsage(left, right ai.Usage) ai.Usage {
	return ai.Usage{
		Input:        left.Input + right.Input,
		Output:       left.Output + right.Output,
		CacheRead:    left.CacheRead + right.CacheRead,
		CacheWrite:   left.CacheWrite + right.CacheWrite,
		CacheWrite1h: addOptional(left.CacheWrite1h, right.CacheWrite1h),
		Reasoning:    addOptional(left.Reasoning, right.Reasoning),
		TotalTokens:  left.TotalTokens + right.TotalTokens,
		Cost: ai.UsageCost{
			Input:      left.Cost.Input + right.Cost.Input,
			Output:     left.Cost.Output + right.Cost.Output,
			CacheRead:  left.Cost.CacheRead + right.Cost.CacheRead,
			CacheWrite: left.Cost.CacheWrite + right.Cost.CacheWrite,
			Total:      left.Cost.Total + right.Cost.Total,
		},
	}
}

func addOptional(left, right *int) *int {
	if left == nil && right == nil {
		return nil
	}
	sum := 0
	if left != nil {
		sum += *left
	}
	if right != nil {
		sum += *right
	}
	return &sum
}
