package serve

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	p "github.com/cristiangirlea/harness/contracts/pluginproto"
)

const hello = `{"type":"hello","protocols":["v9","v1"],"host":{"name":"hrn","version":"dev"}}`
const easy = `{"training":[{"out":3,"y":8,"x":30e-1}],"validation":[{"out":4,"x":4,"y":1}]}`

type host struct {
	t     *testing.T
	in    *io.PipeWriter
	lines chan string
	exit  chan int
	seq   int64
}

func newHost(t *testing.T) *host {
	t.Helper()
	stdin, in := io.Pipe()
	out, stdout := io.Pipe()
	h := &host{t: t, in: in, lines: make(chan string, 8), exit: make(chan int, 1)}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop); in.Close(); out.Close() })
	go func() {
		code := Run(stdin, stdout, io.Discard, "0.1.0")
		stdout.Close()
		h.exit <- code
	}()
	go func() {
		defer close(h.lines)
		r := bufio.NewReader(out)
		for {
			line, err := r.ReadString('\n')
			if len(line) > 0 {
				select {
				case h.lines <- line:
				case <-stop:
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return h
}

func (h *host) send(line string) {
	h.t.Helper()
	if _, err := fmt.Fprintln(h.in, line); err != nil {
		h.t.Fatal(err)
	}
}

func (h *host) next() any {
	h.t.Helper()
	select {
	case line, ok := <-h.lines:
		if !ok || !strings.HasSuffix(line, "\n") {
			h.t.Fatal("missing newline-terminated response")
		}
		frame, err := p.ParsePluginLine([]byte(line))
		if err != nil {
			h.t.Fatal(err)
		}
		var header struct{ Seq int64 }
		if err := json.Unmarshal([]byte(line), &header); err != nil {
			h.t.Fatal(err)
		}
		h.seq++
		if header.Seq != h.seq {
			h.t.Fatalf("seq %d, want %d", header.Seq, h.seq)
		}
		return frame
	case <-time.After(10 * time.Second):
		h.t.Fatal("timed out waiting for frame")
	}
	return nil
}

func (h *host) ready() {
	h.t.Helper()
	h.send(hello)
	f := h.next().(*p.Ready)
	if f.Protocol != p.V1 || f.Plugin != (p.Program{Name: "search", Version: "0.1.0"}) || len(f.Capabilities) != 1 || f.Capabilities[0].Name != "search" || f.Capabilities[0].Description == "" {
		h.t.Fatalf("%+v", f)
	}
}

func (h *host) terminal() any {
	h.t.Helper()
	for {
		f := h.next()
		if e, ok := f.(*p.Event); ok {
			if e.Kind != p.KindProgress || e.ID != "c1" {
				h.t.Fatalf("%+v", e)
			}
			continue
		}
		return f
	}
}

func (h *host) finish(want int) {
	h.t.Helper()
	h.in.Close()
	h.expectExit(want)
}

func (h *host) expectExit(want int) {
	h.t.Helper()
	select {
	case code := <-h.exit:
		if code != want {
			h.t.Fatalf("exit %d, want %d", code, want)
		}
	case <-time.After(10 * time.Second):
		h.t.Fatal("plugin did not exit")
	}
	for extra := range h.lines {
		h.t.Errorf("unexpected trailing frame %q", extra)
	}
}

func call(input string) string {
	return `{"type":"call","id":"c1","capability":"search","input":` + input + "}"
}

func longCall() string {
	var rows []string
	// Even inputs and constants make the odd target unreachable throughout the full budget.
	for i := range 40 {
		rows = append(rows, fmt.Sprintf(`{"x":%d,"y":%d,"out":1}`, 2*(i-18), 2*(i*i%37-15)))
	}
	return call(`{"training":[` + strings.Join(rows, ",") + `],"validation":[{"x":0,"y":0,"out":0}],"constants":[2,6,14,26,46,74,106,158,214,302,394,482,622,734,862,998],"max_depth":5,"max_candidates":5000000}`)
}

func TestUnsupportedVersionsReceiveOneRefusalAndExitTwo(t *testing.T) {
	h := newHost(t)
	h.send(strings.Replace(hello, `"v9","v1"`, `"v9"`, 1))
	f := h.next().(*p.Error)
	if f.Code != p.CodeUnsupportedProtocol || !slices.Equal(f.Protocols, []string{"v1"}) {
		t.Fatalf("%+v", f)
	}
	h.expectExit(2)
}

func TestCallsReportVerdictsAndHashTheDecodedExamplesInOrder(t *testing.T) {
	h := newHost(t)
	h.ready()
	for _, tc := range []struct {
		input, verdict string
		matched        int
		validationJSON string
	}{
		{easy, "verified", 1, `[{"x":4,"y":1,"out":4}]`},
		{strings.Replace(easy, `"out":4`, `"out":5`, 1), "rejected", 0, `[{"x":4,"y":1,"out":5}]`},
		{strings.Replace(easy, `"out":3`, `"max":0`, 1), "", 0, ""},
		{strings.Replace(easy, `"out":3`, `"out":7`, 1)[:len(easy)-1] + `,"max_depth":0}`, "not_found", 0, ""},
	} {
		h.send(call(tc.input))
		r := h.terminal().(*p.Result)
		if r.ID != "c1" {
			t.Fatalf("%+v", r)
		}
		if tc.verdict == "" {
			if r.Status != p.StatusFailed || r.Error.Code != p.CodeBadRequest || !strings.Contains(r.Error.Message, "max") {
				t.Fatalf("%+v", r)
			}
			continue
		}
		var output struct {
			Verdict, Expression                   string
			Depth, Explored, Training, Validation *int
			Matched                               *int            `json:"validation_matched"`
			Mismatch                              json.RawMessage `json:"first_mismatch"`
		}
		if err := json.Unmarshal(r.Output, &output); err != nil {
			t.Fatal(err)
		}
		if r.Status != p.StatusOK || output.Verdict != tc.verdict || output.Training == nil || *output.Training != 1 || output.Validation == nil || *output.Validation != 1 || output.Explored == nil || output.Depth == nil || *output.Depth != 0 {
			t.Fatalf("%+v: %s", r, r.Output)
		}
		if tc.verdict == "not_found" {
			if len(r.Evidence) != 0 || output.Expression != "" || output.Matched != nil || *output.Explored != 2 {
				t.Fatalf("%+v", r)
			}
			continue
		}
		if output.Expression != "x" || *output.Explored != 1 || output.Matched == nil || *output.Matched != tc.matched || (output.Mismatch != nil) != (tc.verdict == "rejected") || len(r.Evidence) != 2 {
			t.Fatalf("%+v: %s", r, r.Output)
		}
		want := map[string]p.Evidence{}
		for _, entry := range []struct {
			kind, source, data string
			matched            int
		}{
			{"training", "training examples", `[{"x":3,"y":8,"out":3}]`, 1},
			{"validation", "held-out examples", tc.validationJSON, tc.matched},
		} {
			want[entry.kind] = p.Evidence{Kind: entry.kind, Source: entry.source, Hash: fmt.Sprintf("%x", sha256.Sum256([]byte(entry.data))), Excerpt: fmt.Sprintf("%d of 1 %s match", entry.matched, entry.source)}
		}
		for _, e := range r.Evidence {
			if expected, ok := want[e.Kind]; !ok || e != expected {
				t.Errorf("evidence %+v, want %+v", e, expected)
			}
			delete(want, e.Kind)
		}
	}
	h.finish(0)
}

func TestBadRequestsLeaveTheSessionAvailableForAnotherCall(t *testing.T) {
	h := newHost(t)
	h.ready()
	for _, line := range []string{`{"type":"future"}`, strings.Repeat(" ", p.MaxLine+1), hello} {
		h.send(line)
		if f := h.next().(*p.Error); f.Code != p.CodeBadRequest {
			t.Fatalf("%+v", f)
		}
	}
	h.send(`{"type":"cancel","id":"other"}`)
	if f := h.next().(*p.Error); f.ID != "other" || f.Code != p.CodeBadRequest {
		t.Fatalf("%+v", f)
	}
	h.send(strings.Replace(call(easy), `"capability":"search"`, `"capability":"rank"`, 1))
	if f := h.next().(*p.Result); f.Status != p.StatusFailed || f.Error.Code != p.CodeUnsupported || f.ID != "c1" {
		t.Fatalf("%+v", f)
	}
	h.send(call(easy))
	if f := h.terminal().(*p.Result); f.Status != p.StatusOK {
		t.Fatalf("%+v", f)
	}
	h.finish(0)
}

func TestCallBeforeHelloAndUnterminatedFinalLine(t *testing.T) {
	h := newHost(t)
	h.send(call(easy))
	if f := h.next().(*p.Error); f.Code != p.CodeBadRequest || f.ID != "c1" || f.Message != "expected hello" {
		t.Fatalf("%+v", f)
	}
	if _, err := io.WriteString(h.in, hello); err != nil {
		t.Fatal(err)
	}
	h.in.Close()
	if f := h.next().(*p.Error); f.Code != p.CodeBadRequest || !strings.Contains(f.Message, "missing its terminating newline") {
		t.Fatalf("%+v", f)
	}
	h.expectExit(0)
}

func TestOverlongLineIsDiscardedAndMaxLineCallSucceeds(t *testing.T) {
	h := newHost(t)
	h.ready()
	h.send(strings.Repeat(" ", p.MaxLine+2))
	if f := h.next().(*p.Error); f.Code != p.CodeBadRequest || f.Message != fmt.Sprintf("line exceeds %d bytes", p.MaxLine) {
		t.Fatalf("%+v", f)
	}
	line := call(easy)
	h.send(line[:len(line)-1] + strings.Repeat(" ", p.MaxLine-len(line)) + "}")
	if f := h.terminal().(*p.Result); f.ID != "c1" || f.Status != p.StatusOK {
		t.Fatalf("%+v", f)
	}
	h.finish(0)
}

func TestLateCancelLeavesSessionReadyForNextCall(t *testing.T) {
	h := newHost(t)
	h.ready()
	h.send(call(easy))
	if f := h.terminal().(*p.Result); f.ID != "c1" || f.Status != p.StatusOK {
		t.Fatalf("%+v", f)
	}
	h.send(`{"type":"cancel","id":"c1"}`)
	if f := h.next().(*p.Error); f.ID != "c1" || f.Code != p.CodeBadRequest {
		t.Fatalf("%+v", f)
	}
	h.send(strings.Replace(call(easy), `"c1"`, `"c2"`, 1))
	if f := h.next().(*p.Result); f.ID != "c2" || f.Status != p.StatusOK {
		t.Fatalf("%+v", f)
	}
	h.finish(0)
}

func TestCancelForAnotherIDLeavesInFlightCallRunning(t *testing.T) {
	h := newHost(t)
	h.ready()
	h.send(longCall())
	if f := h.next().(*p.Event); f.ID != "c1" || f.Kind != p.KindProgress {
		t.Fatalf("%+v", f)
	}
	h.send(`{"type":"cancel","id":"other"}`)
	if f := h.terminal().(*p.Error); f.ID != "other" || f.Code != p.CodeBadRequest {
		t.Fatalf("%+v", f)
	}
	if f := h.terminal().(*p.Result); f.ID != "c1" || f.Status != p.StatusOK {
		t.Fatalf("%+v", f)
	}
	h.finish(0)
}

func TestEvidencePreservesTheReceivedOrderOfHeldOutExamples(t *testing.T) {
	h := newHost(t)
	h.ready()
	const first = `{"x":7,"y":1,"out":1}`
	const second = `{"x":4,"y":9,"out":4}`
	for _, rows := range []string{first + "," + second, second + "," + first} {
		validation := "[" + rows + "]"
		h.send(call(`{"training":[{"x":2,"y":2,"out":2}],"validation":` + validation + "}"))
		result := h.terminal().(*p.Result)
		want := fmt.Sprintf("%x", sha256.Sum256([]byte(validation)))
		if !slices.ContainsFunc(result.Evidence, func(e p.Evidence) bool {
			return e.Kind == "validation" && e.Hash == want && e.Excerpt == "1 of 2 held-out examples match"
		}) {
			t.Fatalf("expected validation digest %s in %+v", want, result.Evidence)
		}
	}
	h.finish(0)
}

func TestAnInvalidPluginVersionExitsOneWithDiagnosticsOnly(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(io.NopCloser(strings.NewReader(hello+"\n")), &stdout, &stderr, "invalid/version")
	if code != p.ExitFailed || stdout.Len() != 0 || !strings.Contains(stderr.String(), "plugin.version") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, &stdout, &stderr)
	}
}

func TestProgressAllowsTheHostToCancelABusySearch(t *testing.T) {
	h := newHost(t)
	h.ready()
	h.send(longCall())
	e := h.next().(*p.Event)
	if e.ID != "c1" || e.Kind != p.KindProgress || !strings.Contains(e.Text, "candidates, depth") {
		t.Fatalf("%+v", e)
	}
	h.send(strings.Replace(call(easy), `"c1"`, `"c2"`, 1))
	if f := h.terminal().(*p.Error); f.Code != p.CodeBusy || f.ID != "c2" {
		t.Fatalf("%+v", f)
	}
	h.send(`{"type":"cancel","id":"c1"}`)
	if f := h.terminal().(*p.Result); f.ID != "c1" || f.Status != p.StatusCanceled {
		t.Fatalf("%+v", f)
	}
	h.finish(0)
}

func TestTimeoutAndClosingInputEachStopTheInFlightCall(t *testing.T) {
	for _, reason := range []string{"timeout", "eof"} {
		t.Run(reason, func(t *testing.T) {
			h := newHost(t)
			h.ready()
			line := longCall()
			if reason == "timeout" {
				line = line[:len(line)-1] + `,"timeout_ms":1}`
			}
			h.send(line)
			if reason == "eof" {
				h.in.Close()
			}
			if f := h.terminal().(*p.Result); f.Status != p.StatusCanceled || f.ID != "c1" {
				t.Fatalf("%+v", f)
			}
			h.finish(0)
		})
	}
}
