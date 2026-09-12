package renderer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestRenderIsolatedAppletError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping isolated render integration test in short mode")
	}

	bin := buildServerBinary(t)
	t.Setenv(RenderWorkerBinEnv(), bin)
	t.Setenv("TRONBYT_RENDER_IN_PROCESS", "")

	star := filepath.Join(t.TempDir(), "fail.star")
	content := "def main(config):\n    fail(\"expected test failure\")\n"
	if err := os.WriteFile(star, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, messages, err := Render(
		context.Background(),
		star,
		nil,
		64, 32,
		15*time.Second,
		30*time.Second,
		true,
		false,
		nil, nil, nil, nil,
	)
	if err == nil {
		t.Fatal("expected render error from failing applet")
	}
	t.Logf("render error: %v", err)
	t.Logf("render messages: %v", messages)
}

func TestRenderIsolatedWorkerSurvivesPanic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping isolated render integration test in short mode")
	}

	bin := buildPanicWorkerBinary(t)
	t.Setenv(RenderWorkerBinEnv(), bin)
	t.Setenv("TRONBYT_RENDER_IN_PROCESS", "")

	_, _, err := Render(
		context.Background(),
		"/nonexistent.star",
		nil,
		64, 32,
		15*time.Second,
		30*time.Second,
		true,
		false,
		nil, nil, nil, nil,
	)
	if err == nil {
		t.Fatal("expected error when worker subprocess panics")
	}
	t.Logf("caller survived worker panic with error: %v", err)
}

func buildServerBinary(t *testing.T) string {
	t.Helper()
	root := moduleRoot(t)
	bin := filepath.Join(t.TempDir(), "tronbyt-server")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/server")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build ./cmd/server: %v\n%s", err, out)
	}
	return bin
}

func buildPanicWorkerBinary(t *testing.T) string {
	t.Helper()
	root := moduleRoot(t)
	bin := filepath.Join(t.TempDir(), "panicworker")
	cmd := exec.Command("go", "build", "-o", bin, "./internal/renderer/testdata/panicworker")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build panicworker: %v\n%s", err, out)
	}
	return bin
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
