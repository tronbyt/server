package renderer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/tronbyt/pixlet/encode"
	"github.com/tronbyt/pixlet/runtime"
)

// Render executes the Starlark script and returns the WebP image bytes.
func Render(
	ctx context.Context,
	script []byte,
	config map[string]string,
	width, height int,
) ([]byte, error) {
	// Initialize Applet
	app, err := runtime.NewApplet("app", script)
	if err != nil {
		return nil, fmt.Errorf("failed to load applet: %w", err)
	}

	// Run
	roots, err := app.RunWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to run applet: %w", err)
	}

	if len(roots) == 0 {
		return nil, fmt.Errorf("applet returned no roots")
	}

	// Create Screens
	screens := encode.ScreensFromRoots(roots, width, height)

	// Encode to WebP
	maxDuration := 15 * time.Second

	webpData, err := screens.EncodeWebP(maxDuration)
	if err != nil {
		return nil, fmt.Errorf("failed to encode webp: %w", err)
	}

	return webpData, nil
}

// GetSchema returns the schema JSON for the given script.
func GetSchema(script []byte) ([]byte, error) {
	app, err := runtime.NewApplet("app", script)
	if err != nil {
		return nil, err
	}

	if app.Schema == nil {
		return []byte("{}"), nil
	}

	return json.Marshal(app.Schema)
}

// CallSchemaHandler executes a schema handler function in the Starlark script.
func CallSchemaHandler(
	ctx context.Context,
	script []byte,
	config map[string]string,
	handlerName string,
	parameter string,
) (string, error) {
	app, err := runtime.NewApplet("app", script)
	if err != nil {
		return "", fmt.Errorf("failed to load applet: %w", err)
	}

	return app.CallSchemaHandler(ctx, handlerName, parameter, config)
}
