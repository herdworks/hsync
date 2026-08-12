// Package cli exposes hsync's reusable command composition.
package cli

import (
	"fmt"

	"github.com/herdworks/clic"
	"github.com/herdworks/hsync"
)

// CLI is hsync's canonical standalone command composition.
var CLI = clic.CLI{
	Command: clic.Command{
		Meta: clic.CommandMeta{
			Name:        hsync.LibName,
			Description: fmt.Sprintf("%s is an HTTP sync tool.", hsync.LibName),
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
	},
	Version: string(hsync.LibVersion),
}

// globals

// --config option
func configOptionMeta() clic.OptionMeta {
	var env string = "HSYNC_CONFIG"
	return clic.OptionMeta{
		Name:        "config",
		ShortName:   "c",
		Description: fmt.Sprintf("Configuration path (or %s / detected hsync config).", env),
		EnvName:     env,
		ShowDefault: true,
	}
}
