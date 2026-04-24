package main

import (
	"log/slog"
	"os"
	"path/filepath"
	_ "time/tzdata"
	"tronbyt-server/cmd/server/boot"
	"tronbyt-server/cmd/server/migrate"
	"tronbyt-server/cmd/server/serve"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func main() {
	// Determine the root command
	var root *cobra.Command
	switch filepath.Base(os.Args[0]) {
	case boot.Name:
		root = boot.New()
	case migrate.Name:
		root = migrate.New()
		root.PreRunE = preRun
	default:
		root = New()
		subCmd, _, err := root.Find(os.Args[1:])
		// Run serve if no command is given
		if err == nil && subCmd.Use == root.Use && subCmd.Flags().Parse(os.Args[1:]) != pflag.ErrHelp {
			root.SetArgs(append([]string{serve.Name}, os.Args[1:]...))
		}
	}

	root.SilenceErrors = true

	if err := root.Execute(); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}
