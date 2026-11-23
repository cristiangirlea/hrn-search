package search

import (
	"context"
	"errors"
	"hash/maphash"
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/cristiangirlea/hrn-search/internal/task"
)

func solve(t *testing.T, p task.Spec) Result {
	t.Helper()
	got, err := Synthesize(t.Context(), p, nil)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestSearchFindsTheSameNontrivialExpressionOnRepeatedRuns(t *testing.T) {
	p := task.Spec{Training: []task.Example{{X: 1, Y: 2, Out: 7}, {X: 2, Y: 3, Out: 13}, {X: 3, Y: 4, Out: 21}},
		Validation: []task.Example{{X: 4, Y: 5, Out: 31}, {X: 5, Y: 7, Out: 47}}, Constants: []int64{2, 1}, MaxDepth: 3, MaxCandidates: 25000}
	got := solve(t, p)
	if got.Expression != "((1+y)+(y*y))" || got.Depth != 2 || got.Explored != 607 ||
		got.Verdict != "rejected" || got.Validation != 2 || got.ValidationMatched == nil || *got.ValidationMatched != 1 {
		t.Fatalf("%+v", got)
	}
	if !slices.Equal(p.Constants, []int64{2, 1}) {
		t.Fatal("mutated constants")
	}
	slices.Reverse(p.Constants)
	if again := solve(t, p); !reflect.DeepEqual(again, got) {
		t.Fatalf("first %+v, second %+v", got, again)
	}
}

func TestOnlyTheFirstTrainingFitIsScoredOnValidation(t *testing.T) {
	p := task.Spec{Training: []task.Example{{X: 2, Y: 2, Out: 2}}, Validation: []task.Example{{X: 7, Y: 1, Out: 1}, {X: 8, Y: 8, Out: 8}, {X: 9, Y: 5, Out: 5}}, MaxDepth: 5, MaxCandidates: 50}
	got := solve(t, p)
	if got.Verdict != "rejected" || got.Expression != "x" || got.Depth != 0 || got.Explored != 1 || got.Training != 1 || got.Validation != 3 || got.ValidationMatched == nil || *got.ValidationMatched != 1 {
		t.Fatalf("%+v", got)
	}
	m := got.FirstMismatch
	if m == nil || m.Index != 0 || m.X != 7 || m.Y != 1 || m.Expected != 1 || m.Actual == nil || *m.Actual != 7 || m.Overflow {
		t.Fatalf("mismatch %+v", m)
	}
	p.Validation = []task.Example{{X: 7, Y: 1, Out: 7}}
	verified := solve(t, p)
	if verified.Verdict != "verified" || verified.FirstMismatch != nil || verified.Expression != got.Expression || verified.Explored != got.Explored {
		t.Fatalf("%+v", verified)
	}
}

func TestExhaustingEitherLimitPreservesTheLastAttemptedDepthAndCount(t *testing.T) {
	p := task.Spec{Training: []task.Example{{X: 1, Y: 1, Out: 1000}}, Validation: []task.Example{{X: 2, Y: 2, Out: 1000}}, MaxDepth: 0, MaxCandidates: 10}
	for _, tc := range []struct{ depth, budget, explored, lastDepth int }{{0, 10, 2, 0}, {3, 1, 1, 0}, {3, 3, 3, 1}} {
		p.MaxDepth, p.MaxCandidates = tc.depth, tc.budget
		got := solve(t, p)
		if got.Verdict != "not_found" || got.Explored != tc.explored || got.Depth != tc.lastDepth || got.Expression != "" || got.ValidationMatched != nil || got.FirstMismatch != nil {
			t.Fatalf("%+v", got)
		}
	}
	p.MaxCandidates = 0
	if _, err := Synthesize(t.Context(), p, nil); err == nil {
		t.Fatal("accepted empty budget")
	}
}

func TestCancellationReturnsTheContextError(t *testing.T) {
	p := task.Spec{Training: []task.Example{{X: 1, Y: 2, Out: 1000}}, Validation: []task.Example{{}}, MaxDepth: 3, MaxCandidates: 1000}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Synthesize(ctx, p, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCandidateBudgetCanOutlastTheRetainedTermLimit(t *testing.T) {
	// Even leaves make an odd target unreachable at every depth.
	s := enumerator{
		ctx: t.Context(), training: []task.Example{{X: 1018, Y: 1126, Out: 1}}, budget: 530_000,
		seed: maphash.MakeSeed(), seen: make(map[uint64]*expr), values: make([]byte, 8),
	}
	constants := []int64{2, 6, 14, 26, 46, 74, 106, 158, 214, 302, 394, 482, 622, 734, 862, 998}
	if _, found := s.firstFit(constants, 3); found || s.explored != s.budget {
		t.Fatalf("found %v after %d of %d candidates", found, s.explored, s.budget)
	}
	if got := len(s.terms); got != maxRetainedTerms || got > s.budget {
		t.Fatalf("retained %d terms, limit %d, budget %d", got, maxRetainedTerms, s.budget)
	}
}

func TestOverflowCannotVerifyWrappedArithmetic(t *testing.T) {
	for _, tc := range []struct {
		op    byte
		a, b  int64
		valid bool
	}{
		{'+', math.MaxInt64, 1, false}, {'+', math.MinInt64, -1, false}, {'+', math.MinInt64, 0, true},
		{'-', math.MinInt64, 1, false}, {'-', math.MaxInt64, -1, false}, {'-', math.MaxInt64, 1, true},
		{'*', math.MaxInt64, 2, false}, {'*', math.MinInt64, -1, false}, {'*', -1, math.MinInt64, false}, {'*', math.MinInt64, 1, true}, {'*', math.MinInt64, 0, true},
	} {
		if _, ok := apply(tc.op, tc.a, tc.b); ok != tc.valid {
			t.Errorf("%d %c %d valid = %v", tc.a, tc.op, tc.b, ok)
		}
	}
	p := task.Spec{Training: []task.Example{{X: 3, Y: 7, Out: 6}, {X: 2, Y: 9, Out: 4}}, Validation: []task.Example{{X: math.MaxInt64, Out: -2}}, MaxDepth: 1, MaxCandidates: 100}
	got := solve(t, p)
	if got.Verdict != "rejected" || got.FirstMismatch == nil || !got.FirstMismatch.Overflow || got.FirstMismatch.Actual != nil {
		t.Fatalf("%+v", got)
	}
}
