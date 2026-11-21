package renderer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/tronbyt/pixlet/encode"
	"github.com/tronbyt/pixlet/runtime"
	"github.com/tronbyt/pixlet/runtime/modules/host"
	"github.com/tronbyt/pixlet/runtime/modules/render_runtime/canvas"
	"github.com/tronbyt/pixlet/server/loader"
	"golang.org/x/text/language"
)

// Render executes the Starlark app at path within root and returns the WebP image bytes.
// If result is not nil, it is filled in with what the app returned besides the
// image, such as a requested sound.
func Render(
	ctx context.Context,
	root *os.Root,
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
	hostInfo host.Info,
	result *loader.RenderResult,
) ([]byte, []string, error) {
	location := time.Local
	if timezone != nil && *timezone != "" {
		v, err := time.LoadLocation(*timezone)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid timezone: %v", err)
		}
		location = v
	}

	lang := language.English
	if locale != nil && *locale != "" {
		var err error
		lang, err = language.Parse(*locale)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid locale: %v", err)
		}
	}

	var renderFilters encode.RenderFilters
	for _, f := range filters {
		var cf encode.ColorFilter
		if err := cf.UnmarshalText([]byte(f)); err == nil {
			renderFilters.ColorFilter = cf
		}
	}

	r, err := loader.RenderAppletRoot(
		ctx, root, path, config,
		loader.WithMeta(canvas.Metadata{
			Width:  width,
			Height: height,
			Is2x:   output2x,
		}),
		loader.WithMaxDuration(maxDuration),
		loader.WithTimeout(timeout),
		loader.WithImageFormat(loader.ImageWebP),
		loader.WithSilenceOutput(silenceOutput),
		loader.WithLocation(location),
		loader.WithLanguage(lang),
		loader.WithFilters(renderFilters),
		loader.WithShowFullAnimation(showFullAnimation),
		loader.WithHostInfo(hostInfo),
	)
	if result != nil {
		*result = r
	}
	return r.Image, r.Output, err
}

// GetSchema returns the schema JSON for the app at path within root.
func GetSchema(ctx context.Context, root *os.Root, path string, width, height int, output2x bool, hostInfo host.Info) ([]byte, error) {
	applet, err := runtime.NewAppletFromRoot(
		ctx, root, path,
		runtime.WithCanvasMeta(canvas.Metadata{
			Width:  width,
			Height: height,
			Is2x:   output2x,
		}),
		runtime.WithHostInfo(hostInfo),
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

// CallSchemaHandler executes a schema handler function in the app at path within root.
func CallSchemaHandler(
	ctx context.Context,
	root *os.Root,
	path string,
	config map[string]any,
	width, height int,
	output2x bool,
	handlerName string,
	parameter string,
	hostInfo host.Info,
) (string, error) {
	applet, err := runtime.NewAppletFromRoot(
		ctx, root, path,
		runtime.WithCanvasMeta(canvas.Metadata{
			Width:  width,
			Height: height,
			Is2x:   output2x,
		}),
		runtime.WithHostInfo(hostInfo),
	)
	if err != nil {
		return "", fmt.Errorf("failed to load applet: %w", err)
	}
	defer func() {
		err := applet.Close()
		if err != nil {
			slog.Error("failed to close applet", "error", err)
		}
	}()

	return applet.CallSchemaHandler(ctx, handlerName, parameter, config)
}
