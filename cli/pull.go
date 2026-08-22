package cli

import (
	"fmt"

	"github.com/herdworks/clic"
	"github.com/herdworks/hsync"
)

// pull command
func PullCommand(config hsync.ConfigManager) clic.Command {
	if config == nil {
		config = hsync.DefaultConfigManager{}
	}
	configOption := GlobalConfigOption()
	return clic.Command{
		Meta: clic.CommandMeta{
			Description: "Pull remote changes into the workspace.",
			Usage:       []string{"{{ .Command }} [directory] [--config path]"},
		},
		Spec: clic.CommandSpec{
			Key: "pull",
			Options: clic.Options{
				configOption.Spec.Key: &configOption,
			},
			Help:      clic.DefaultHelp,
			Validator: pullValidator,
			Handler: func(execution *clic.Execution) error {
				return pullHandler(execution, config)
			},
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

	return nil
}

// pull handler
func pullHandler(execution *clic.Execution, manager hsync.ConfigManager) (err error) {
	var operation *hsync.PullOperation
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
	if operation, err = hsync.PullWithConfig(execution.Context, config, execution.Stdout); err != nil {
		return err
	} else if operation.Status == hsync.StatusNoop {
		_, err := fmt.Fprintln(execution.Stdout, "Already up to date.")
		return err
	}
	_, err = fmt.Fprintln(execution.Stdout, operation.Remote.Head.ID())
	return err
}
