package serve

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	p "github.com/cristiangirlea/harness/contracts/pluginproto"
)

type incoming struct {
	frame   any
	bad     error
	readErr error
}

func readHost(r *bufio.Reader) incoming {
	var line []byte
	tooLong := false
	for {
		part, err := r.ReadSlice('\n')
		if !tooLong {
			if len(part)+len(line) > p.MaxLine+2 {
				tooLong = true
			} else {
				line = append(line, part...)
			}
		}
		switch err {
		case bufio.ErrBufferFull:
			continue
		case nil:
			if tooLong {
				return incoming{bad: fmt.Errorf("line exceeds %d bytes", p.MaxLine)}
			}
			f, err := p.ParseHostLine(line)
			return incoming{frame: f, bad: err}
		case io.EOF:
			if len(line) != 0 || tooLong {
				return incoming{bad: errors.New("line missing its terminating newline")}
			}
		}
		return incoming{readErr: err}
	}
}

type writer struct {
	out io.Writer
	seq int64
}

func (w *writer) send(frame any) error {
	w.seq++
	switch f := frame.(type) {
	case *p.Ready:
		f.V, f.Seq, f.Type = 1, w.seq, p.TypeReady
	case *p.Error:
		f.V, f.Seq, f.Type = 1, w.seq, p.TypeError
	case *p.Event:
		f.V, f.Seq, f.Type = 1, w.seq, p.TypeEvent
	case *p.Result:
		f.V, f.Seq, f.Type = 1, w.seq, p.TypeResult
	default:
		return fmt.Errorf("unexpected outgoing frame %T", frame)
	}
	line, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if _, err := p.ParsePluginLine(line); err != nil {
		return err
	}
	n, err := w.out.Write(line)
	if err == nil && n != len(line) {
		return io.ErrShortWrite
	}
	return err
}
