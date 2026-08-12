package cli

import (
	"fmt"

	"github.com/herdworks/clic"
	"github.com/herdworks/hsync"
)

// status command
func StatusCommand() clic.Command {
	return clic.Command{
		Meta: StatusMeta(),
		Spec: StatusSpec(),
	}
}

func StatusMeta() clic.CommandMeta {
	return clic.CommandMeta{
		Name:        "status",
		Description: "List modified files in the workspace.",
		Usage:       []string{"hsync status [directory] [--config path]"},
	}
}

func StatusSpec() clic.CommandSpec {
	config := StatusConfigOption()
	return clic.CommandSpec{
		Key:       "status",
		Options:   clic.Options{"config": &config},
		Help:      clic.DefaultHelp,
		Validator: statusValidator,
		Handler:   statusHandler,
	}
}

// status --config option
func StatusConfigOption() clic.Option {
	return clic.Option{
		Meta: StatusConfigMeta(),
		Spec: StatusConfigSpec(),
	}
}

func StatusConfigMeta() clic.OptionMeta { return configOptionMeta() }

func StatusConfigSpec() clic.OptionSpec {
	return clic.OptionSpec{
		Key:  "config",
		Kind: clic.StringOption,
	}
}

// status validator
func statusValidator(execution *clic.Execution) error {
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

// status handler
func statusHandler(execution *clic.Execution) error {
	// Use the positional directory as the working directory, if provided.
	if execution.Command.PositionalArguments() == 1 {
		execution.WorkingDirectory = execution.Command.State.Arguments[0]
	}
	config, err := hsync.LoadSyncConfig(execution.WorkingDirectory, execution.Options["config"].StringValue())
	if err != nil {
		return err
	}
	repository, err := hsync.OpenRepository(config.Directory())
	if err != nil {
		return err
	}
	repository.Config = config.Sync
	result, err := repository.Status()
	if err != nil {
		return err
	}
	for _, change := range result.Changes {
		if _, err := fmt.Fprintf(execution.Stdout, "%10.10s %s\n", change.Kind, change.Path); err != nil {
			return err
		}
	}
	return nil
}
