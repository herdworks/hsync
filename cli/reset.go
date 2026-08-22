package cli

import (
	"fmt"

	"github.com/herdworks/clic"
	"github.com/herdworks/hsync"
)

// reset command
func ResetCommand(config hsync.ConfigManager) clic.Command {
	if config == nil {
		config = hsync.DefaultConfigManager{}
	}
	configOption, interactive := GlobalConfigOption(), ResetInteractiveOption()
	return clic.Command{
		Meta: clic.CommandMeta{
			Description: "Reset modified files in the workspace.",
			Usage:       []string{"{{ .Command }} [directory] [--config path] [--interactive]"},
		},
		Spec: clic.CommandSpec{
			Key:  "reset",
			Help: clic.DefaultHelp,
			Options: clic.Options{
				"config":      &configOption,
				"interactive": &interactive,
			},
			Validator: resetValidator,
			Handler: func(execution *clic.Execution) error {
				return resetHandler(execution, config)
			},
		},
	}
}

// reset --interactive option
func ResetInteractiveOption() clic.Option {
	return clic.Option{
		Meta: clic.OptionMeta{
			ShortName:   "i",
			Description: "Choose each tracked file to restore.",
		},
		Spec: clic.OptionSpec{
			Key:  "interactive",
			Kind: clic.BoolOption,
		},
	}
}

// reset validator
func resetValidator(execution *clic.Execution) error {
	if execution.Command.PositionalArguments() > 1 {
		return fmt.Errorf("%s accepts at most one directory", execution.Command.Meta.Name)
	}
	return nil
}

// reset handler
func resetHandler(execution *clic.Execution, manager hsync.ConfigManager) error {
	directory, configPath := "", execution.Options["config"].StringValue()
	if execution.Command.PositionalArguments() == 1 {
		directory = execution.Command.State.Arguments[0]
	} else if configPath == "" {
		directory = execution.WorkingDirectory
	}
	config, err := manager.Read(directory, configPath)
	if err != nil {
		return err
	}
	operation, err := hsync.ResetWithConfig(
		execution.Context,
		config,
		execution.Stdout,
		nil,
		execution.Options["interactive"].BoolValue(),
	)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(execution.Stdout, operation.Repository.Base.ID())
	return err
}
