package cli

import (
	"fmt"

	"github.com/herdworks/clic"
	"github.com/herdworks/hsync"
)

// init command
func InitCommand(config hsync.ConfigManager) clic.Command {
	if config == nil {
		config = hsync.DefaultConfigManager{}
	}
	configOption := GlobalConfigOption()
	configOption.Meta.Description = "Configuration file to create (or ${{ .EnvName }})."
	configOption.Meta.Default = config.DefaultConfigPath()
	configOption.Meta.ShowDefault = false
	verbose := clic.Option{
		Meta: clic.OptionMeta{
			Description: "Write the manager's effective synchronization configuration.",
		},
		Spec: clic.OptionSpec{
			Key:  "verbose",
			Kind: clic.BoolOption,
		},
	}
	return clic.Command{
		Meta: clic.CommandMeta{
			Description: "Initialize a website for synchronization.",
			Usage:       []string{"{{ .Command }} <remote> [--config hsync.yaml] [--verbose]"},
		},
		Spec: clic.CommandSpec{
			Key: "init",
			Options: clic.Options{
				configOption.Spec.Key: &configOption,
				verbose.Spec.Key:      &verbose,
			},
			Help:      clic.DefaultHelp,
			Validator: initValidator,
			Handler: func(execution *clic.Execution) error {
				return initHandler(execution, config)
			},
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
func initHandler(execution *clic.Execution, config hsync.ConfigManager) error {
	return config.Initialize(execution.WorkingDirectory, execution.Options["config"].StringValue(), execution.Command.State.Arguments[0], execution.Options["verbose"].BoolValue())
}
