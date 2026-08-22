package cli

import (
	"fmt"

	"github.com/herdworks/clic"
	"github.com/herdworks/hsync"
)

// RemoteCommand returns the remote command composition.
func RemoteCommand(config hsync.ConfigManager) clic.Command {
	if config == nil {
		config = hsync.DefaultConfigManager{}
	}
	configOption := GlobalConfigOption()
	indexURL := clic.Option{
		Meta: clic.OptionMeta{
			Description: "Print the resolved synchronization index URL.",
		},
		Spec: clic.OptionSpec{
			Key:  "index-url",
			Kind: clic.BoolOption,
		},
	}
	return clic.Command{
		Meta: clic.CommandMeta{
			Description: "Print the configured synchronization remote.",
			Usage:       []string{"{{ .Command }} [directory] [--config path] [--index-url]"},
		},
		Spec: clic.CommandSpec{
			Key: "remote",
			Options: clic.Options{
				configOption.Spec.Key: &configOption,
				indexURL.Spec.Key:     &indexURL,
			},
			Help:      clic.DefaultHelp,
			Validator: remoteValidator,
			Handler: func(execution *clic.Execution) error {
				return remoteHandler(execution, config)
			},
		},
	}
}

func remoteValidator(execution *clic.Execution) error {
	if execution.Command.PositionalArguments() > 1 {
		return fmt.Errorf("%s accepts at most one directory", execution.Command.Meta.Name)
	}
	return nil
}

func remoteHandler(execution *clic.Execution, manager hsync.ConfigManager) error {
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
	remote, err := hsync.RemoteWithConfig(config, execution.Options["index-url"].BoolValue())
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(execution.Stdout, remote)
	return err
}
