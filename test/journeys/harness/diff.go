package harness

import (
	"strings"
)

// lineDiff renders a compact unified-style diff of two texts (rows prefixed - for a, + for b). It returns "" when the texts are equal.
func lineDiff(a, b string) string {
	if a == b {
		return ""
	}
	x := strings.Split(a, "\n")
	y := strings.Split(b, "\n")
	n, m := len(x), len(y)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out []string
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && x[i] == y[j]:
			out = append(out, "  "+x[i])
			i++
			j++
		case j < m && (i == n || lcs[i][j+1] >= lcs[i+1][j]):
			out = append(out, "+ "+y[j])
			j++
		default:
			out = append(out, "- "+x[i])
			i++
		}
	}
	// Keep two rows of context around each change.
	keep := make([]bool, len(out))
	for k, l := range out {
		if l[0] != ' ' {
			for d := -2; d <= 2; d++ {
				if k+d >= 0 && k+d < len(out) {
					keep[k+d] = true
				}
			}
		}
	}
	var b2 strings.Builder
	skipped := false
	for k, l := range out {
		if !keep[k] {
			skipped = true
			continue
		}
		if skipped {
			b2.WriteString("  ...\n")
			skipped = false
		}
		b2.WriteString(l + "\n")
	}
	return strings.TrimRight(b2.String(), "\n")
}
