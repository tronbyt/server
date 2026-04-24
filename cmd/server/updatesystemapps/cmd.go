package updatesystemapps

import (
	"fmt"
	"tronbyt-server/internal/config"
	"tronbyt-server/internal/gitutils"

	"github.com/spf13/cobra"
)

func New() *cobra.Command {
	return &cobra.Command{
		Use:   "update-system-apps",
		Short: "Updates the system apps repo.",
		RunE:  run,
	}
}

func run(cmd *cobra.Command, _ []string) error {
	cfg, err := config.FromContext(cmd.Context())
	if err != nil {
		return fmt.Errorf("failed to load settings: %w", err)
	}

	// Clone/Update System Apps Repo
	if err := gitutils.EnsureRepo(cfg.SystemAppsDir(), cfg.SystemAppsRepo, cfg.GitHubToken, true); err != nil {
		return fmt.Errorf("failed to update system apps repo: %w", err)
	}
	return nil
}
