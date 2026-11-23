package serve

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	p "github.com/cristiangirlea/harness/contracts/pluginproto"
)

func TestPinnedHostFixturesAreAcceptedByTheServingLoop(t *testing.T) {
	goTool, err := exec.LookPath("go")
	if errors.Is(err, exec.ErrNotFound) {
		t.Skip("go is not on PATH; cannot locate pinned protocol fixtures")
	}
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), goTool, "list", "-m", "-f", "{{.Dir}}", "github.com/cristiangirlea/harness/contracts").CombinedOutput()
	if err != nil {
		t.Fatalf("locating contracts: %v: %s", err, out)
	}
	dir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(dir) {
		t.Fatalf("missing contracts directory: %q", dir)
	}
	root := filepath.Join(dir, "pluginproto", "testdata", "v1")
	counts := map[string]int{}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var header struct{ Type, ID string }
		if err := json.Unmarshal(data, &header); err != nil {
			return err
		}
		switch header.Type {
		case p.TypeHello, p.TypeCall, p.TypeCancel:
			counts[header.Type]++
		default:
			if filepath.Base(filepath.Dir(path)) == "host" {
				t.Errorf("unexpected host fixture %s", path)
			}
			return nil
		}
		t.Run(filepath.Base(path), func(t *testing.T) {
			h := newHost(t)
			if header.Type != p.TypeHello {
				h.ready()
			}
			if header.Type == p.TypeCancel {
				h.send(strings.Replace(longCall(), `"id":"c1"`, `"id":"`+header.ID+`"`, 1))
				if f := h.next().(*p.Event); f.Kind != p.KindProgress || f.ID != header.ID {
					t.Fatalf("unexpected progress %+v", f)
				}
			}
			h.send(strings.TrimSuffix(string(data), "\n"))
			switch header.Type {
			case p.TypeHello:
				if f := h.next().(*p.Ready); f.Protocol != p.V1 {
					t.Fatalf("%+v", f)
				}
			case p.TypeCall:
				if f := h.terminal().(*p.Result); f.Status != p.StatusFailed || f.Error.Code != p.CodeBadRequest {
					t.Fatalf("%+v", f)
				}
			case p.TypeCancel:
				if f := h.terminal().(*p.Result); f.Status != p.StatusCanceled || f.ID != header.ID {
					t.Fatalf("%+v", f)
				}
			}
			h.finish(0)
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{p.TypeHello, p.TypeCall, p.TypeCancel} {
		if counts[kind] == 0 {
			t.Errorf("no %s fixtures in %s", kind, root)
		}
	}
}
