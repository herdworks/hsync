// Package cli exposes hsync's reusable command composition.
package cli

import (
	"fmt"

	"github.com/herdworks/clic"
	"github.com/herdworks/hsync"
)

// CLI is hsync's canonical standalone command composition.
var CLI = clic.CLI{
	Command: Command(nil),
	Version: string(hsync.LibVersion),
}

// Command returns hsync's canonical command composition using config.
func Command(config hsync.ConfigManager) clic.Command {
	return clic.Command{
		Meta: clic.CommandMeta{
			Description: fmt.Sprintf("%s is an HTTP sync tool.", hsync.LibName),
			Usage:       []string{"{{ .Command }} [command] [options]"},
			EnvPrefix:   "HSYNC_",
		},
		Spec: clic.CommandSpec{
			Key:  hsync.LibName,
			Help: clic.DefaultHelp,
		},
		Commands: clic.MustCommands(
			DownloadCommand(),
			InitCommand(config),
			PullCommand(config),
			PushCommand(config),
			RemoteCommand(config),
			ResetCommand(config),
			StatusCommand(config),
		),
	}
}

// GlobalConfigOption returns the reusable --config option for existing repositories.
func GlobalConfigOption() clic.Option {
	return clic.Option{
		Meta: clic.OptionMeta{
			ShortName:   "c",
			Description: "Configuration path (or ${{ .EnvName }} / detected hsync config).",
			EnvName:     "{{ .EnvPrefix }}CONFIG",
			ShowDefault: true,
		},
		Spec: clic.OptionSpec{
			Key:  "config",
			Kind: clic.StringOption,
		},
	}
}
