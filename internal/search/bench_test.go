package search

import (
	"math/rand"
	"testing"

	"github.com/cristiangirlea/hrn-search/internal/task"
)

func TestReferenceExpressionsVerifyWithinTheirCandidateBudgets(t *testing.T) {
	// Budgets allow the preceding breadth-first layers and bound work in the target layer.
	for _, tc := range []struct {
		name          string
		depth, budget int
		reference     func(x, y int64) int64
	}{
		{"x", 0, 1, func(x, y int64) int64 { return x }},
		{"y", 0, 2, func(x, y int64) int64 { return y }},
		{"2", 0, 4, func(x, y int64) int64 { return 2 }},
		{"x+y", 1, 20, func(x, y int64) int64 { return x + y }},
		{"x-y", 1, 20, func(x, y int64) int64 { return x - y }},
		{"y-x", 1, 40, func(x, y int64) int64 { return y - x }},
		{"x*y", 1, 20, func(x, y int64) int64 { return x * y }},
		{"x*x-y", 2, 1500, func(x, y int64) int64 { return x*x - y }},
		{"x*y+1", 2, 1500, func(x, y int64) int64 { return x*y + 1 }},
		{"(x+1)*(y+2)", 2, 2000, func(x, y int64) int64 { return (x + 1) * (y + 2) }},
		{"(x-y)*(x+y)", 2, 1500, func(x, y int64) int64 { return (x - y) * (x + y) }},
		{"x-((x+1)*(y+2))", 3, 10000, func(x, y int64) int64 { return x - ((x + 1) * (y + 2)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			examples := func(seed int64, offset, width int) []task.Example {
				rng := rand.New(rand.NewSource(seed))
				rows := make([]task.Example, 16)
				for i := range rows {
					x, y := int64(offset+rng.Intn(width)), int64(offset+rng.Intn(width))
					rows[i] = task.Example{X: x, Y: y, Out: tc.reference(x, y)}
				}
				return rows
			}
			p := task.Spec{Training: examples(71, -9, 19), Validation: examples(572, 20, 31), Constants: []int64{1, 2}, MaxDepth: tc.depth, MaxCandidates: tc.budget}
			got := solve(t, p)
			t.Logf("explored %d of %d candidates at depth %d", got.Explored, tc.budget, got.Depth)
			if got.Verdict != "verified" || got.Explored > tc.budget {
				t.Fatalf("%+v", got)
			}
		})
	}
}
