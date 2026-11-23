package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	p "github.com/cristiangirlea/harness/contracts/pluginproto"
)

func TestBuiltPluginAnswersARealHandshakeAndSearch(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "hrn-search")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-ldflags", "-X main.version=0.1.0", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			cancel()
			cmd.Wait()
		}
	}()
	reader := bufio.NewReader(out)
	exchange := func(line string) any {
		t.Helper()
		if _, err := fmt.Fprintln(in, line); err != nil {
			t.Fatal(err)
		}
		reply, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		f, err := p.ParsePluginLine(reply)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	ready := exchange(`{"type":"hello","protocols":["v1"],"host":{"name":"hrn","version":"dev"}}`).(*p.Ready)
	if ready.Seq != 1 || ready.Plugin.Version != "0.1.0" || ready.Plugin.Name != "search" {
		t.Fatalf("%+v", ready)
	}
	result := exchange(`{"type":"call","id":"real","capability":"search","input":{"training":[{"x":2,"y":3,"out":5}],"validation":[{"x":7,"y":4,"out":11}]}}`).(*p.Result)
	var output struct{ Verdict, Expression string }
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if result.Seq != 2 || result.ID != "real" || result.Status != p.StatusOK || output.Verdict != "verified" || output.Expression != "(x+y)" {
		t.Fatalf("%+v: %s", result, result.Output)
	}
	if err := in.Close(); err != nil {
		t.Fatal(err)
	}
	if extra, err := reader.ReadByte(); err == nil {
		t.Fatalf("unexpected output byte %q", extra)
	}
	err = cmd.Wait()
	waited = true
	if err != nil {
		t.Fatalf("plugin: %v: %s", err, &diagnostics)
	}
}
