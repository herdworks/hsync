package cli

import (
	"fmt"

	"github.com/herdworks/clic"
	"github.com/herdworks/hsync"
)

// pull command
func PullCommand() clic.Command {
	return clic.Command{
		Meta: PullMeta(),
		Spec: PullSpec(),
	}
}

func PullMeta() clic.CommandMeta {
	return clic.CommandMeta{
		Name:        "pull",
		Description: "Pull remote changes into the workspace.",
		Usage:       []string{"hsync pull [directory] [--config path]"},
	}
}

func PullSpec() clic.CommandSpec {
	config := PullConfigOption()
	return clic.CommandSpec{
		Key:       "pull",
		Options:   clic.Options{"config": &config},
		Help:      clic.DefaultHelp,
		Validator: pullValidator,
		Handler:   pullHandler,
	}
}

// pull --config option
func PullConfigOption() clic.Option {
	return clic.Option{
		Meta: PullConfigMeta(),
		Spec: PullConfigSpec(),
	}
}

func PullConfigMeta() clic.OptionMeta {
	return configOptionMeta()
}

func PullConfigSpec() clic.OptionSpec {
	return clic.OptionSpec{
		Key:  "config",
		Kind: clic.StringOption,
	}
}

// pull validator
func pullValidator(execution *clic.Execution) error {
	// ensure no more than one directory configured
	if execution.Command.PositionalArguments() > 1 {
		return fmt.Errorf(
			"%s accepts at most one directory",
			execution.Command.Meta.Name,
		)
	}

	// validate configuration
	if execution.Command.PositionalArguments() > 0 && execution.Options["config"].StringValue() != "" {
		return fmt.Errorf(
			"%s cannot combine a directory argument with -%s/--%s or $%s",
			execution.Command.Meta.Name,
			execution.Options["config"].Meta.ShortName,
			execution.Options["config"].Meta.Name,
			execution.Options["config"].Meta.EnvName,
		)
	}
	return nil
}

// pull handler
func pullHandler(execution *clic.Execution) error {
	// Use the positional directory as the working directory, if provided.
	if execution.Command.PositionalArguments() == 1 {
		execution.WorkingDirectory = execution.Command.State.Arguments[0]
	}
	config, err := hsync.LoadSyncConfig(execution.WorkingDirectory, execution.Options["config"].StringValue())
	if err != nil {
		return err
	}
	result, err := hsync.PullWithOptions(execution.Context, config.Directory(), *config.Sync, hsync.PullOptions{Output: execution.Stdout})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(execution.Stdout, result.ManifestID)
	return err
}
