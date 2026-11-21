package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteThenRead(t *testing.T) {
	dir := t.TempDir()
	r := Default(dir)

	out, isErr := r.Call(context.Background(), "write_file", `{"path":"a/b.txt","content":"hello"}`)
	if isErr {
		t.Fatalf("write_file: %s", out)
	}
	out, isErr = r.Call(context.Background(), "read_file", `{"path":"a/b.txt"}`)
	if isErr || out != "hello" {
		t.Fatalf("read_file = %q, %v", out, isErr)
	}
}

func TestPathEscape(t *testing.T) {
	r := Default(t.TempDir())
	for _, p := range []string{"../x", "/etc/passwd", "a/../../x"} {
		out, isErr := r.Call(context.Background(), "read_file", `{"path":"`+p+`"}`)
		if !isErr {
			t.Errorf("%s: expected error, got %q", p, out)
		}
	}
}

func TestBash(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Default(dir)

	out, isErr := r.Call(context.Background(), "bash", `{"command":"ls"}`)
	if isErr || strings.TrimSpace(out) != "f" {
		t.Fatalf("ls = %q, %v", out, isErr)
	}
	out, isErr = r.Call(context.Background(), "bash", `{"command":"echo oops; exit 3"}`)
	if !isErr || !strings.Contains(out, "oops") || !strings.Contains(out, "exit code 3") {
		t.Fatalf("exit 3 = %q, %v", out, isErr)
	}
}

func TestBashTimeout(t *testing.T) {
	r := NewRegistry(Bash{Workdir: t.TempDir(), Timeout: 200 * time.Millisecond})
	out, isErr := r.Call(context.Background(), "bash", `{"command":"sleep 5"}`)
	if !isErr || !strings.Contains(out, "timed out") {
		t.Fatalf("sleep = %q, %v", out, isErr)
	}
}

func TestUnknownTool(t *testing.T) {
	out, isErr := Default(t.TempDir()).Call(context.Background(), "nope", `{}`)
	if !isErr || !strings.Contains(out, "unknown tool") {
		t.Fatalf("got %q, %v", out, isErr)
	}
}
