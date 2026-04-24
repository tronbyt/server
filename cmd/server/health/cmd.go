package health

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"
)

func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "health [url]",
		Short: "Performs a health check against the running server",
		RunE:  run,

		SilenceUsage: true,
	}
	return cmd
}

func run(cmd *cobra.Command, args []string) error {
	url := "http://localhost:8000/health"
	if len(args) > 0 {
		url = args[0]
	}

	if err := runHealthCheck(cmd.Context(), url); err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}

	return nil
}

func runHealthCheck(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to perform request: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status code: %d", resp.StatusCode)
	}
	return nil
}
