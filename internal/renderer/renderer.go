package renderer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/tronbyt/pixlet/runtime"
	"github.com/tronbyt/pixlet/runtime/modules/render_runtime/canvas"
)

// Render executes the Starlark script and returns the WebP image bytes.
// Rendering runs in a subprocess so panics inside pixlet's parallel frame
// workers cannot take down the server process.
func Render(
	ctx context.Context,
	path string,
	config map[string]any,
	width, height int,
	maxDuration time.Duration,
	timeout time.Duration,
	silenceOutput bool,
	output2x bool,
	timezone *string,
	locale *string,
	filters []string,
	showFullAnimation *bool,
) ([]byte, []string, error) {
	if shouldRenderInProcess() {
		return renderInProcess(
			ctx, path, config,
			width, height,
			maxDuration, timeout,
			silenceOutput, output2x,
			timezone, locale,
			filters, showFullAnimation,
		)
	}

	return renderIsolated(
		ctx, path, config,
		width, height,
		maxDuration, timeout,
		silenceOutput, output2x,
		timezone, locale,
		filters, showFullAnimation,
	)
}

func shouldRenderInProcess() bool {
	if os.Getenv("TRONBYT_RENDER_IN_PROCESS") == "1" {
		return true
	}
	// Isolated rendering is opt-in: only the server binary sets this in main.
	// go test binaries never do, so they always render in-process.
	return os.Getenv(RenderWorkerBinEnv()) == ""
}

// GetSchema returns the schema JSON for the given script.
func GetSchema(ctx context.Context, path string, width, height int, output2x bool) ([]byte, error) {
	applet, err := runtime.NewAppletFromPath(
		ctx, path,
		runtime.WithCanvasMeta(canvas.Metadata{
			Width:  width,
			Height: height,
			Is2x:   output2x,
		}),
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		err := applet.Close()
		if err != nil {
			slog.Error("failed to close applet", "error", err)
		}
	}()

	if applet.Schema == nil {
		return []byte("{}"), nil
	}

	return json.Marshal(applet.Schema)
}

// CallSchemaHandler executes a schema handler function in the Starlark script.
func CallSchemaHandler(
	ctx context.Context,
	path string,
	config map[string]any,
	width, height int,
	output2x bool,
	handlerName string,
	parameter string,
) (string, error) {
	applet, err := runtime.NewAppletFromPath(
		ctx, path,
		runtime.WithCanvasMeta(canvas.Metadata{
			Width:  width,
			Height: height,
			Is2x:   output2x,
		}),
	)
	if err != nil {
		return "", fmt.Errorf("failed to load applet from path: %w", err)
	}
	defer func() {
		err := applet.Close()
		if err != nil {
			slog.Error("failed to close applet", "error", err)
		}
	}()

	return applet.CallSchemaHandler(ctx, handlerName, parameter, config)
}
