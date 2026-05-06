/*
Copyright © 2025 srz_zumix
*/
package cmd

import (
	"context"
	"os"
	"os/signal"

	"github.com/spf13/cobra"
	"github.com/srz-zumix/gh-diet-kit/version"
	"github.com/srz-zumix/go-gh-extension/pkg/actions"
	"github.com/srz-zumix/go-gh-extension/pkg/gh/guardrails"
	"github.com/srz-zumix/go-gh-extension/pkg/logger"
)

var (
	logLevel string
	readOnly bool
)

var rootCmd = &cobra.Command{
	Use:     "gh-diet-kit",
	Short:   "A slim GitHub CLI extension based on gh-team-kit",
	Long:    `A slim GitHub CLI extension based on gh-team-kit`,
	Version: version.Version,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		logger.SetLogLevel(logLevel)
		guardrails.NewGuardrail(guardrails.ReadOnlyOption(readOnly))
	},
}

func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err := rootCmd.ExecuteContext(ctx)
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	if actions.IsRunsOn() {
		rootCmd.SetErrPrefix(actions.GetErrorPrefix())
	}
	logger.AddCmdFlag(rootCmd, rootCmd.PersistentFlags(), &logLevel, "log-level", "L")
	rootCmd.PersistentFlags().BoolVar(&readOnly, "read-only", false, "Run in read-only mode (prevent write operations)")
}
