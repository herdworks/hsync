package cli

import (
	"fmt"

	"github.com/herdworks/clic"
	"github.com/herdworks/hsync"
)

// status command
func StatusCommand(config hsync.ConfigManager) clic.Command {
	if config == nil {
		config = hsync.DefaultConfigManager{}
	}
	configOption := GlobalConfigOption()
	verbose := clic.Option{
		Meta: clic.OptionMeta{
			Description: "Also list unmodified, ignored, and limited included files.",
		},
		Spec: clic.OptionSpec{
			Key:  "verbose",
			Kind: clic.BoolOption,
		},
	}
	return clic.Command{
		Meta: clic.CommandMeta{
			Description: "List modified files in the workspace.",
			Usage:       []string{"{{ .Command }} [directory] [--config path]"},
		},
		Spec: clic.CommandSpec{
			Key: "status",
			Options: clic.Options{
				configOption.Spec.Key: &configOption,
				verbose.Spec.Key:      &verbose,
			},
			Help:      clic.DefaultHelp,
			Validator: statusValidator,
			Handler: func(execution *clic.Execution) error {
				return statusHandler(execution, config)
			},
		},
	}
}

// status validator
func statusValidator(execution *clic.Execution) error {
	if execution.Command.PositionalArguments() > 1 {
		return fmt.Errorf("%s accepts at most one directory", execution.Command.Meta.Name)
	}
	return nil
}

// status handler
func statusHandler(execution *clic.Execution, manager hsync.ConfigManager) error {
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
	result, err := hsync.StatusWithConfig(config)
	if err != nil {
		return err
	}
	verbose := execution.Options["verbose"].BoolValue()
	if verbose {
		for _, entry := range result.Unmodified {
			if _, err := fmt.Fprintf(execution.Stdout, "%12.12s %s\n", "unmodified", entry.Path); err != nil {
				return err
			}
		}
	}
	added, modified, deleted := 0, 0, 0
	for _, change := range result.Changes {
		switch change.Kind {
		case hsync.ChangeAdded:
			added++
		case hsync.ChangeModified:
			modified++
		case hsync.ChangeDeleted:
			deleted++
		}
		if _, err := fmt.Fprintf(execution.Stdout, "%12.12s %s\n", change.Kind, change.Path); err != nil {
			return err
		}
	}
	if !verbose {
		_, err = fmt.Fprintf(execution.Stdout, "\n%d added, %d modified, %d deleted\n", added, modified, deleted)
		return err
	}
	for _, entry := range result.Ignored {
		if _, err := fmt.Fprintf(execution.Stdout, "%12.12s %s\n", "ignored", entry.Path); err != nil {
			return err
		}
	}
	for _, entry := range result.Limited {
		if _, err := fmt.Fprintf(execution.Stdout, "%12.12s %s\n", "limited", entry.Path); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(execution.Stdout, "\n%d unmodified, %d added, %d modified, %d ignored, %d limited, %d deleted\n", len(result.Unmodified), added, modified, len(result.Ignored), len(result.Limited), deleted)
	return err
}
