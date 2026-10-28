package scan

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ParseSize reads a memory amount like "2G", "500M", "1.5GB" or "800mb".
// A bare number is taken as megabytes.
func ParseSize(s string) (int64, error) {
	u := strings.ToUpper(strings.TrimSpace(s))
	u = strings.TrimSuffix(strings.TrimSuffix(u, "B"), "I") // 2GB, 2GiB
	mult := float64(1 << 20)
	switch {
	case strings.HasSuffix(u, "T"):
		mult, u = 1<<40, strings.TrimSuffix(u, "T")
	case strings.HasSuffix(u, "G"):
		mult, u = 1<<30, strings.TrimSuffix(u, "G")
	case strings.HasSuffix(u, "M"):
		mult, u = 1<<20, strings.TrimSuffix(u, "M")
	case strings.HasSuffix(u, "K"):
		mult, u = 1<<10, strings.TrimSuffix(u, "K")
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(u), 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("can't read %q as an amount of memory (try 2G or 500M)", s)
	}
	return int64(n * mult), nil
}

// PlanFree picks the most forgotten targets (highest score first, then the
// biggest) until want bytes are covered. Targets scoring under minScore are
// never picked. total may fall short of want if there isn't enough.
func PlanFree(ts []*Target, want int64, minScore int) (picked []*Target, total int64) {
	cands := make([]*Target, 0, len(ts))
	for _, t := range ts {
		if t.Stray && t.Score >= minScore && t.TreeRSS > 0 {
			cands = append(cands, t)
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].Score != cands[j].Score {
			return cands[i].Score > cands[j].Score
		}
		return cands[i].TreeRSS > cands[j].TreeRSS
	})
	for _, t := range cands {
		if total >= want {
			break
		}
		picked = append(picked, t)
		total += t.TreeRSS
	}
	return picked, total
}
