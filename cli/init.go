package cli

import (
	"fmt"

	"github.com/herdworks/clic"
	"github.com/herdworks/hsync"
)

// init command
func InitCommand() clic.Command {
	config := GlobalConfigOption()
	config.Meta.Description = "Configuration file to create (or ${{ .EnvName }})."
	config.Meta.Default = hsync.DefaultConfigPath
	config.Meta.ShowDefault = false
	return clic.Command{
		Meta: clic.CommandMeta{
			Description: "Initialize a website for synchronization.",
			Usage:       []string{"{{ .Command }} <remote> [--config hsync.yaml]"},
		},
		Spec: clic.CommandSpec{
			Key: "init",
			Options: clic.Options{
				config.Spec.Key: &config,
			},
			Help:      clic.DefaultHelp,
			Validator: initValidator,
			Handler:   initHandler,
		},
	}
}

// init validation
func initValidator(execution *clic.Execution) error {
	if len(execution.Command.State.Arguments) != 1 {
		return fmt.Errorf("init requires one remote URL")
	}
	return nil
}

// init handler
func initHandler(execution *clic.Execution) error {
	return hsync.InitializeConfig(execution.Options["config"].StringValue(), execution.Command.State.Arguments[0])
}
