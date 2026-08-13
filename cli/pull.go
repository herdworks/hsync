package cli

import (
	"fmt"

	"github.com/herdworks/clic"
	"github.com/herdworks/hsync"
)

// pull command
func PullCommand() clic.Command {
	config := GlobalConfigOption()
	return clic.Command{
		Meta: clic.CommandMeta{
			Description: "Pull remote changes into the workspace.",
			Usage:       []string{"{{ .Command }} [directory] [--config path]"},
		},
		Spec: clic.CommandSpec{
			Key: "pull",
			Options: clic.Options{
				config.Spec.Key: &config,
			},
			Help:      clic.DefaultHelp,
			Validator: pullValidator,
			Handler:   pullHandler,
		},
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
