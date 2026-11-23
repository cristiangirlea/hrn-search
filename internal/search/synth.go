package search

import (
	"context"
	"encoding/binary"
	"hash/maphash"
	"math"
	"slices"
	"strconv"

	"github.com/cristiangirlea/hrn-search/internal/task"
)

type Mismatch struct {
	Index    int    `json:"index"`
	X        int64  `json:"x"`
	Y        int64  `json:"y"`
	Expected int64  `json:"expected"`
	Actual   *int64 `json:"actual,omitempty"`
	Overflow bool   `json:"overflow,omitempty"`
}

type Result struct {
	Verdict           string    `json:"verdict"`
	Expression        string    `json:"expression,omitempty"`
	Depth             int       `json:"depth"`
	Explored          int       `json:"explored"`
	Training          int       `json:"training"`
	Validation        int       `json:"validation"`
	ValidationMatched *int      `json:"validation_matched,omitempty"`
	FirstMismatch     *Mismatch `json:"first_mismatch,omitempty"`
}

type expr struct {
	op          byte
	constant    int64
	left, right *expr
	text        string
	collision   *expr
}

func (e expr) format() string {
	switch e.op {
	case 'x', 'y':
		return string(e.op)
	case 0:
		return strconv.FormatInt(e.constant, 10)
	default:
		return "(" + e.left.text + string(e.op) + e.right.text + ")"
	}
}

func apply(op byte, a, b int64) (int64, bool) {
	switch op {
	case '+':
		c := a + b
		return c, (b <= 0 || c >= a) && (b >= 0 || c <= a)
	case '-':
		c := a - b
		return c, (b <= 0 || c < a) && (b >= 0 || c > a)
	default:
		if a == 0 || b == 0 {
			return 0, true
		}
		if (a == math.MinInt64 && b == -1) || (b == math.MinInt64 && a == -1) {
			return 0, false
		}
		c := a * b
		return c, c/b == a
	}
}

func (e *expr) eval(x, y int64) (int64, bool) {
	switch e.op {
	case 'x':
		return x, true
	case 'y':
		return y, true
	case 0:
		return e.constant, true
	}
	a, ok := e.left.eval(x, y)
	if !ok {
		return 0, false
	}
	b, ok := e.right.eval(x, y)
	if !ok {
		return 0, false
	}
	return apply(e.op, a, b)
}

func Synthesize(ctx context.Context, p task.Spec, progress func(explored, depth int)) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := p.Validate(); err != nil {
		return Result{}, err
	}
	s := enumerator{
		ctx: ctx, training: p.Training, budget: p.MaxCandidates, progress: progress,
		seen: make(map[uint64]*expr), seed: maphash.MakeSeed(), values: make([]byte, 8*len(p.Training)),
	}
	e, found := s.firstFit(p.Constants, p.MaxDepth)
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	result := Result{Verdict: "not_found", Depth: s.depth, Explored: s.explored, Training: len(p.Training), Validation: len(p.Validation)}
	if !found {
		return result, nil
	}
	result.Verdict = "verified"
	result.Expression = e.format()
	matched := 0
	for i, row := range p.Validation {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		actual, ok := e.eval(row.X, row.Y)
		if ok && actual == row.Out {
			matched++
			continue
		}
		if result.FirstMismatch == nil {
			m := &Mismatch{Index: i, X: row.X, Y: row.Y, Expected: row.Out, Overflow: !ok}
			if ok {
				m.Actual = &actual
			}
			result.FirstMismatch = m
			result.Verdict = "rejected"
		}
	}
	result.ValidationMatched = &matched
	return result, nil
}

const maxRetainedTerms = 1 << 16

type enumerator struct {
	ctx                     context.Context
	training                []task.Example
	budget, explored, depth int
	progress                func(int, int)
	terms                   []*expr
	seen                    map[uint64]*expr
	seed                    maphash.Seed
	values                  []byte
}

func (s *enumerator) firstFit(constants []int64, maxDepth int) (expr, bool) {
	base := []expr{{op: 'x'}, {op: 'y'}}
	constants = slices.Clone(constants)
	slices.Sort(constants)
	for _, n := range constants {
		base = append(base, expr{constant: n})
	}
	for _, e := range base {
		if s.try(e, 0, maxDepth > 0) {
			return e, true
		}
		if s.explored == s.budget || s.ctx.Err() != nil {
			return expr{}, false
		}
	}
	start := 0
	for depth := 1; depth <= maxDepth && start < len(s.terms); depth++ {
		end := len(s.terms)
		// Each ordered pair has subtraction; each unordered pair has addition and multiplication.
		n, old := int64(end), int64(start)
		layerSize := 2*(n*n-old*old) + (n - old)
		keep := depth < maxDepth && layerSize < int64(s.budget-s.explored)
		for i, l := range s.terms[:end] {
			first := 0
			if i < start {
				first = start
			}
			for _, r := range s.terms[first:end] {
				for _, op := range [...]byte{'+', '-', '*'} {
					if op != '-' && l.text > r.text {
						continue
					}
					e := expr{op: op, left: l, right: r}
					if s.try(e, depth, keep && len(s.terms) < maxRetainedTerms) {
						return e, true
					}
					if s.explored == s.budget || s.ctx.Err() != nil {
						return expr{}, false
					}
				}
			}
		}
		start = end
	}
	return expr{}, false
}

func (s *enumerator) try(e expr, depth int, keep bool) bool {
	s.explored++
	s.depth = depth
	if s.progress != nil && s.explored%4096 == 0 {
		s.progress(s.explored, depth)
	}
	match := true
	for i, row := range s.training {
		v, ok := e.eval(row.X, row.Y)
		if !ok {
			return false
		}
		if v != row.Out {
			if !keep {
				return false
			}
			match = false
		}
		if keep {
			binary.LittleEndian.PutUint64(s.values[i*8:], uint64(v))
		}
	}
	if match {
		return true
	}
	// Recheck hash collisions to keep distinct training behaviours without storing every vector.
	key := maphash.Bytes(s.seed, s.values)
	for prev := s.seen[key]; prev != nil; prev = prev.collision {
		if s.sameValues(prev) {
			return false
		}
	}
	retained := e
	retained.text = e.format()
	retained.collision = s.seen[key]
	s.seen[key] = &retained
	s.terms = append(s.terms, &retained)
	return false
}

func (s *enumerator) sameValues(e *expr) bool {
	for i, row := range s.training {
		v, _ := e.eval(row.X, row.Y)
		if uint64(v) != binary.LittleEndian.Uint64(s.values[i*8:]) {
			return false
		}
	}
	return true
}
