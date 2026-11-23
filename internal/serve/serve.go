package serve

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	p "github.com/cristiangirlea/harness/contracts/pluginproto"
)

func Run(in io.ReadCloser, out, diagnostics io.Writer, version string) int {
	defer in.Close()
	code, err := run(bufio.NewReader(in), &writer{out: out}, version)
	if err != nil {
		fmt.Fprintln(diagnostics, err)
		return p.ExitFailed
	}
	return code
}

func run(r *bufio.Reader, w *writer, version string) (int, error) {
	// A refused handshake must exit even when the host holds stdin open.
	for {
		line := readHost(r)
		if errors.Is(line.readErr, io.EOF) {
			return p.ExitOK, nil
		}
		if line.readErr != nil {
			return p.ExitFailed, line.readErr
		}
		if hello, ok := line.frame.(*p.Hello); ok {
			supported := []string{p.V1}
			protocol, ok := p.Negotiate(hello.Protocols, supported)
			if !ok {
				return p.ExitRefused, w.send(&p.Error{Code: p.CodeUnsupportedProtocol, Message: "search speaks v1", Protocols: supported})
			}
			ready := &p.Ready{
				Protocol:     protocol,
				Plugin:       p.Program{Name: "search", Version: version},
				Capabilities: []p.Capability{{Name: "search", Description: "Find an integer expression that fits training examples."}},
			}
			if err := w.send(ready); err != nil {
				return p.ExitFailed, err
			}
			break
		}
		var id string
		message := "expected hello"
		switch f := line.frame.(type) {
		case *p.Call:
			id = f.ID
		case *p.Cancel:
			id = f.ID
		default:
			message = fmt.Sprintf("%.512s", line.bad)
		}
		if err := w.send(badRequest(id, message)); err != nil {
			return p.ExitFailed, err
		}
	}
	input := make(chan incoming)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			line := readHost(r)
			select {
			case input <- line:
			case <-stop:
				return
			}
			if line.readErr != nil {
				return
			}
		}
	}()
	return session(input, w)
}

type progress struct{ explored, depth int }

func session(input <-chan incoming, w *writer) (int, error) {
	var id string
	var cancel context.CancelFunc
	var updates <-chan progress
	var done <-chan outcome
	defer func() {
		if cancel != nil {
			cancel()
			<-done
		}
	}()
	for {
		var reply any
		select {
		case line := <-input:
			if line.readErr != nil {
				if !errors.Is(line.readErr, io.EOF) {
					return p.ExitFailed, line.readErr
				}
				if cancel == nil {
					return p.ExitOK, nil
				}
				cancel()
				input = nil
				continue
			}
			switch f := line.frame.(type) {
			case *p.Hello:
				reply = badRequest("", "handshake already completed")
			case *p.Call:
				switch {
				case cancel != nil:
					reply = &p.Error{Code: p.CodeBusy, ID: f.ID, Message: "a call is in flight"}
				case f.Capability != "search":
					reply = failed(f.ID, p.CodeUnsupported, "unknown capability "+f.Capability)
				default:
					id = f.ID
					var ctx context.Context
					ctx, cancel = callContext(f.TimeoutMS)
					progressCh := make(chan progress)
					resultCh := make(chan outcome, 1)
					updates, done = progressCh, resultCh
					go func() { resultCh <- searchCall(ctx, f, progressCh) }()
					continue
				}
			case *p.Cancel:
				if cancel == nil || f.ID != id {
					reply = badRequest(f.ID, "no call with that id is in flight")
					break
				}
				cancel()
				continue
			default:
				reply = badRequest("", fmt.Sprintf("%.512s", line.bad))
			}
		case u := <-updates:
			reply = &p.Event{ID: id, Kind: p.KindProgress, Text: fmt.Sprintf("explored %d candidates, depth %d", u.explored, u.depth)}
		case result := <-done:
			cancel()
			cancel, updates, done = nil, nil, nil
			if result.err != nil {
				return p.ExitFailed, result.err
			}
			reply = result.frame
		}
		if err := w.send(reply); err != nil {
			return p.ExitFailed, err
		}
		if input == nil && cancel == nil {
			return p.ExitOK, nil
		}
	}
}

func callContext(ms int64) (context.Context, context.CancelFunc) {
	if ms == 0 {
		return context.WithCancel(context.Background())
	}
	ms = min(ms, math.MaxInt64/int64(time.Millisecond))
	return context.WithTimeout(context.Background(), time.Duration(ms)*time.Millisecond)
}

func badRequest(id, message string) *p.Error {
	return &p.Error{Code: p.CodeBadRequest, ID: id, Message: message}
}

func failed(id, code, message string) *p.Result {
	return &p.Result{ID: id, Status: p.StatusFailed, Error: &p.Problem{Code: code, Message: message}}
}
