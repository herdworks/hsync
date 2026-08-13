package cli

import (
	"fmt"

	"github.com/herdworks/clic"
	"github.com/herdworks/hsync"
)

// reset command
func ResetCommand() clic.Command {
	config, interactive := GlobalConfigOption(), ResetInteractiveOption()
	return clic.Command{
		Meta: clic.CommandMeta{
			Description: "Reset modified files in the workspace.",
			Usage:       []string{"{{ .Command }} [directory] [--config path] [--interactive]"},
		},
		Spec: clic.CommandSpec{
			Key:  "reset",
			Help: clic.DefaultHelp,
			Options: clic.Options{
				"config":      &config,
				"interactive": &interactive,
			},
			Validator: resetValidator,
			Handler:   resetHandler,
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
	config := execution.Options["config"]
	if execution.Command.PositionalArguments() > 0 && config.StringValue() != "" {
		return fmt.Errorf(
			"%s cannot combine a directory argument with -%s/--%s or $%s",
			execution.Command.Meta.Name,
			config.Meta.ShortName,
			config.Meta.Name,
			config.Meta.EnvName,
		)
	}
	return nil
}

// reset handler
func resetHandler(execution *clic.Execution) error {
	// Use the positional directory as the working directory, if provided.
	if execution.Command.PositionalArguments() == 1 {
		execution.WorkingDirectory = execution.Command.State.Arguments[0]
	}
	config, err := hsync.LoadSyncConfig(execution.WorkingDirectory, execution.Options["config"].StringValue())
	if err != nil {
		return err
	}
	repository, remote, err := hsync.OpenCheckout(config.Directory(), *config.Sync, nil)
	if err != nil {
		return err
	}
	result, err := repository.Reset(execution.Context, remote, hsync.ResetOptions{Output: execution.Stdout, Interactive: execution.Options["interactive"].BoolValue()})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(execution.Stdout, result.ManifestID)
	return err
}
