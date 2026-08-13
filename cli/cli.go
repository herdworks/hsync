// Package cli exposes hsync's reusable command composition.
package cli

import (
	"fmt"

	"github.com/herdworks/clic"
	"github.com/herdworks/hsync"
)

// CLI is hsync's canonical standalone command composition.
var CLI = clic.CLI{
	Command: Command(),
	Version: string(hsync.LibVersion),
}

// Command returns hsync's canonical command composition.
func Command() clic.Command {
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
		Commands: clic.MustCommandsFrom(
			DownloadCommand,
			InitCommand,
			PullCommand,
			PushCommand,
			ResetCommand,
			StatusCommand,
		),
	}
}

// GlobalConfigOption returns the reusable --config option for existing checkouts.
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
