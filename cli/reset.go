package cli

import (
	"fmt"

	"github.com/herdworks/clic"
	"github.com/herdworks/hsync"
)

// reset command
func ResetCommand() clic.Command {
	return clic.Command{
		Meta: ResetMeta(),
		Spec: ResetSpec(),
	}
}

func ResetMeta() clic.CommandMeta {
	return clic.CommandMeta{
		Name:        "reset",
		Description: "Reset modified files in the workspace.",
		Usage:       []string{"hsync reset [directory] [--config path] [--interactive]"},
	}
}

func ResetSpec() clic.CommandSpec {
	config, interactive := ResetConfigOption(), ResetInteractiveOption()
	return clic.CommandSpec{
		Key:  "reset",
		Help: clic.DefaultHelp,
		Options: clic.Options{
			"config":      &config,
			"interactive": &interactive,
		},
		Validator: func(execution *clic.Execution) error {
			if execution.Command.PositionalArguments() > 1 {
				return fmt.Errorf("reset accepts at most one directory")
			}
			if execution.Command.PositionalArguments() > 0 && execution.Options["config"].StringValue() != "" {
				return fmt.Errorf("reset cannot combine a directory with --config or HSYNC_CONFIG")
			}
			return nil
		},
		Handler: resetHandler,
	}
}

// reset --config option
func ResetConfigOption() clic.Option {
	return clic.Option{
		Meta: ResetConfigMeta(),
		Spec: ResetConfigSpec(),
	}
}

func ResetConfigMeta() clic.OptionMeta { return configOptionMeta() }

func ResetConfigSpec() clic.OptionSpec {
	return clic.OptionSpec{Key: "config", Kind: clic.StringOption}
}

// reset --interactive option
func ResetInteractiveOption() clic.Option {
	return clic.Option{
		Meta: ResetInteractiveMeta(),
		Spec: ResetInteractiveSpec(),
	}
}

func ResetInteractiveMeta() clic.OptionMeta {
	return clic.OptionMeta{
		Name:        "interactive",
		ShortName:   "i",
		Description: "Choose each tracked file to restore.",
	}
}

func ResetInteractiveSpec() clic.OptionSpec {
	return clic.OptionSpec{Key: "interactive", Kind: clic.BoolOption}
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
