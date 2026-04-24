package resetpassword

import (
	"context"
	"fmt"
	"log/slog"
	"tronbyt-server/internal/auth"
	"tronbyt-server/internal/config"
	"tronbyt-server/internal/data"

	"github.com/spf13/cobra"
	"gorm.io/gorm"
)

func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reset-password username new_password",
		Short: "Resets the password for a specified user",
		RunE:  run,
		Args:  cobra.ExactArgs(2),
	}

	return cmd
}

func run(cmd *cobra.Command, args []string) error {
	cfg, err := config.FromContext(cmd.Context())
	if err != nil {
		return fmt.Errorf("failed to load settings: %w", err)
	}

	username := args[0]
	password := args[1]

	if err := resetPassword(cmd.Context(), cfg.DBDSN, username, password); err != nil {
		slog.Error("Failed to reset password", "error", err)
		return err
	}

	slog.Info("Password reset successfully")
	return nil
}

func resetPassword(ctx context.Context, dsn, username, password string) error {
	db, err := data.Open(dsn, "INFO")
	if err != nil {
		return err
	}

	hashedPassword, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	rowsAffected, err := gorm.G[data.User](db).Where("username = ?", username).Update(ctx, "password", hashedPassword)
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}
