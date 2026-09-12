package renderer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/tronbyt/pixlet/encode"
	"github.com/tronbyt/pixlet/runtime/modules/render_runtime/canvas"
	"github.com/tronbyt/pixlet/server/loader"
	"golang.org/x/text/language"
)

const renderWorkerEnv = "TRONBYT_RENDER_WORKER"
const renderWorkerBinEnv = "TRONBYT_RENDER_WORKER_BIN"

// RenderWorkerEnv returns the environment variable that marks a render worker subprocess.
func RenderWorkerEnv() string {
	return renderWorkerEnv
}

// RenderWorkerBinEnv returns the environment variable that points at the server
// binary used to spawn isolated render workers. Only cmd/server/main sets this.
func RenderWorkerBinEnv() string {
	return renderWorkerBinEnv
}

type renderRequest struct {
	Path              string         `json:"path"`
	Config            map[string]any `json:"config"`
	Width             int            `json:"width"`
	Height            int            `json:"height"`
	MaxDurationNS     int64          `json:"maxDurationNs"`
	TimeoutNS         int64          `json:"timeoutNs"`
	SilenceOutput     bool           `json:"silenceOutput"`
	Output2x          bool           `json:"output2x"`
	Timezone          *string        `json:"timezone,omitempty"`
	Locale            *string        `json:"locale,omitempty"`
	Filters           []string       `json:"filters,omitempty"`
	ShowFullAnimation *bool          `json:"showFullAnimation,omitempty"`
}

type renderResponse struct {
	Image    []byte   `json:"image,omitempty"`
	Messages []string `json:"messages,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// RunWorker executes a render request from stdin and writes a JSON response to stdout.
// It is invoked by re-executing the server binary with TRONBYT_RENDER_WORKER=1.
func RunWorker() {
	var req renderRequest
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		writeWorkerResponse(renderResponse{Error: err.Error()})
		os.Exit(1)
	}

	ctx := context.Background()
	if req.TimeoutNS > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutNS))
		defer cancel()
	}

	// Always capture applet print() output into messages. stdout is reserved
	// for the JSON worker response; uncaptured prints would corrupt it.
	img, messages, err := renderInProcess(
		ctx,
		req.Path,
		req.Config,
		req.Width,
		req.Height,
		time.Duration(req.MaxDurationNS),
		time.Duration(req.TimeoutNS),
		true,
		req.Output2x,
		req.Timezone,
		req.Locale,
		req.Filters,
		req.ShowFullAnimation,
	)
	if err != nil {
		writeWorkerResponse(renderResponse{Messages: messages, Error: err.Error()})
		os.Exit(1)
	}

	writeWorkerResponse(renderResponse{Image: img, Messages: messages})
}

func writeWorkerResponse(resp renderResponse) {
	_ = json.NewEncoder(os.Stdout).Encode(resp)
}

func renderInProcess(
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

	return loader.RenderApplet(
		ctx, path, config,
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
	)
}

func renderIsolated(
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
	exe := os.Getenv(renderWorkerBinEnv)
	if exe == "" {
		return renderInProcess(
			ctx, path, config,
			width, height,
			maxDuration, timeout,
			silenceOutput, output2x,
			timezone, locale,
			filters, showFullAnimation,
		)
	}

	req := renderRequest{
		Path:              path,
		Config:            config,
		Width:             width,
		Height:            height,
		MaxDurationNS:     maxDuration.Nanoseconds(),
		TimeoutNS:         timeout.Nanoseconds(),
		SilenceOutput:     true, // capture prints into response messages
		Output2x:          output2x,
		Timezone:          timezone,
		Locale:            locale,
		Filters:           filters,
		ShowFullAnimation: showFullAnimation,
	}

	var reqBody bytes.Buffer
	if err := json.NewEncoder(&reqBody).Encode(req); err != nil {
		return nil, nil, fmt.Errorf("encode render request: %w", err)
	}

	cmd := exec.CommandContext(ctx, exe)
	cmd.Env = append(os.Environ(), renderWorkerEnv+"=1")
	cmd.Stdin = &reqBody

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		var resp renderResponse
		if decodeErr := json.NewDecoder(&stdout).Decode(&resp); decodeErr == nil && resp.Error != "" {
			return nil, resp.Messages, fmt.Errorf("%s", resp.Error)
		}
		return nil, nil, fmt.Errorf("render worker failed: %w", err)
	}

	var resp renderResponse
	if err := json.NewDecoder(&stdout).Decode(&resp); err != nil {
		return nil, nil, fmt.Errorf("decode render response: %w", err)
	}
	if resp.Error != "" {
		return nil, resp.Messages, fmt.Errorf("%s", resp.Error)
	}

	return resp.Image, resp.Messages, nil
}
