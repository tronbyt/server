package renderer

import (
	"os"
	"testing"
)

func TestShouldRenderInProcessWithoutWorkerBin(t *testing.T) {
	t.Setenv(RenderWorkerBinEnv(), "")
	t.Setenv("TRONBYT_RENDER_IN_PROCESS", "")

	if !shouldRenderInProcess() {
		t.Fatal("expected in-process rendering when worker binary is unset")
	}
}

func TestShouldRenderInProcessWhenForced(t *testing.T) {
	t.Setenv(RenderWorkerBinEnv(), "/usr/bin/tronbyt-server")
	t.Setenv("TRONBYT_RENDER_IN_PROCESS", "1")

	if !shouldRenderInProcess() {
		t.Fatal("expected in-process rendering when TRONBYT_RENDER_IN_PROCESS=1")
	}
}

func TestShouldRenderIsolatedWithWorkerBin(t *testing.T) {
	t.Setenv(RenderWorkerBinEnv(), "/usr/bin/tronbyt-server")
	t.Setenv("TRONBYT_RENDER_IN_PROCESS", "")

	if shouldRenderInProcess() {
		t.Fatal("expected isolated rendering when worker binary is set")
	}
}

func TestRenderWorkerBinUnsetInTests(t *testing.T) {
	if os.Getenv(RenderWorkerBinEnv()) != "" {
		t.Fatalf("test binary should not set %s", RenderWorkerBinEnv())
	}
}
