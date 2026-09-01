package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/neilmpatterson/em-tui/internal/config"
	"github.com/neilmpatterson/em-tui/internal/tui"
	"github.com/spf13/cobra"
)

var cfgPath string
var forceSetup bool

var rootCmd = &cobra.Command{
	Use:   "em-tui",
	Short: "EM Toolkit — terminal UI for engineering managers",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgPath)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		app := tui.New(cfg, cfgPath, forceSetup)
		p := tea.NewProgram(app, tea.WithAltScreen())
		_, err = p.Run()
		return err
	},
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgPath, "config", "c", "", "path to config file (default: ~/.config/em-tui/config.yaml)")
	rootCmd.PersistentFlags().BoolVar(&forceSetup, "setup", false, "re-run the setup wizard")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
