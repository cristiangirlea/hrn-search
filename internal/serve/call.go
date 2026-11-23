package serve

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	p "github.com/cristiangirlea/harness/contracts/pluginproto"
	"github.com/cristiangirlea/hrn-search/internal/search"
	"github.com/cristiangirlea/hrn-search/internal/task"
)

type outcome struct {
	frame *p.Result
	err   error
}

func searchCall(ctx context.Context, call *p.Call, updates chan<- progress) outcome {
	canceled := outcome{frame: &p.Result{ID: call.ID, Status: p.StatusCanceled}}
	spec, err := task.Decode(call.Input)
	if ctx.Err() != nil {
		return canceled
	}
	if err != nil {
		return outcome{frame: failed(call.ID, p.CodeBadRequest, err.Error())}
	}
	var last time.Time
	report := func(explored, depth int) {
		if time.Since(last) < 250*time.Millisecond {
			return
		}
		select {
		case updates <- progress{explored, depth}:
			last = time.Now()
		case <-ctx.Done():
		}
	}
	res, err := search.Synthesize(ctx, spec, report)
	if ctx.Err() != nil {
		return canceled
	}
	if err != nil {
		return outcome{err: err}
	}
	frame := &p.Result{ID: call.ID, Status: p.StatusOK}
	frame.Output, err = json.Marshal(res)
	if err != nil {
		return outcome{err: err}
	}
	if res.ValidationMatched != nil {
		for _, set := range []struct {
			kind, source string
			rows         []task.Example
			matched      int
		}{
			{"validation", "held-out examples", spec.Validation, *res.ValidationMatched},
			{"training", "training examples", spec.Training, len(spec.Training)},
		} {
			data, err := json.Marshal(set.rows)
			if err != nil {
				return outcome{err: err}
			}
			frame.Evidence = append(frame.Evidence, p.Evidence{Kind: set.kind, Source: set.source, Hash: fmt.Sprintf("%x", sha256.Sum256(data)), Excerpt: fmt.Sprintf("%d of %d %s match", set.matched, len(set.rows), set.source)})
		}
	}
	if ctx.Err() != nil {
		return canceled
	}
	return outcome{frame: frame}
}
