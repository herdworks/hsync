package cli

import (
	"fmt"
	"strings"

	"github.com/herdworks/clic"
	"github.com/herdworks/hsync"
)

// push command
func PushCommand(config hsync.ConfigManager) clic.Command {
	if config == nil {
		config = hsync.DefaultConfigManager{}
	}
	configOption := GlobalConfigOption()
	environment := PushEnvironmentOption()
	providers := PushProvidersOption()
	return clic.Command{
		Meta: clic.CommandMeta{
			Description: "Push local changes to the remote.",
			Usage: []string{
				"{{ .Command }}",
				"{{ .Command }} [--config path]",
				"{{ .Command }} [--config path] [--environment environment]",
				"{{ .Command }} [--config path] [--providers provider,provider]",
			},
		},
		Spec: clic.CommandSpec{
			Key:  "push",
			Help: clic.DefaultHelp,
			Options: clic.Options{
				"config":      &configOption,
				"environment": &environment,
				"providers":   &providers,
			},
			Validator: pushValidator,
			Handler: func(execution *clic.Execution) error {
				return pushHandler(execution, config)
			},
		},
	}
}

// push --environment option
func PushEnvironmentOption() clic.Option {
	return clic.Option{
		Meta: clic.OptionMeta{
			ShortName:   "e",
			Description: "Deployment environment (default: hsync).",
		},
		Spec: clic.OptionSpec{
			Key:  "environment",
			Kind: clic.StringOption,
		},
	}
}

// push --providers option
func PushProvidersOption() clic.Option {
	return clic.Option{
		Meta: clic.OptionMeta{
			ShortName:   "p",
			Description: "Comma-separated provider names.",
		},
		Spec: clic.OptionSpec{
			Key:  "providers",
			Kind: clic.StringOption,
		},
	}
}

// push validation
func pushValidator(execution *clic.Execution) error {
	if execution.Command.PositionalArguments() != 0 {
		return fmt.Errorf(
			"%s accepts no positional arguments; use -%s/--%s to select a repository",
			execution.Command.Meta.Name,
			execution.Options["config"].Meta.ShortName,
			execution.Options["config"].Meta.Name,
		)
	}
	if execution.Options["environment"].StringValue() != "" && execution.Options["providers"].StringValue() != "" {
		return fmt.Errorf(
			"-%s/--%s and -%s/--%s are mutually exclusive",
			execution.Options["providers"].Meta.ShortName,
			execution.Options["providers"].Meta.Name,
			execution.Options["environment"].Meta.ShortName,
			execution.Options["environment"].Meta.Name,
		)
	}
	return nil
}

// push handler
func pushHandler(execution *clic.Execution, manager hsync.ConfigManager) (err error) {
	var operation *hsync.PushOperation
	directory, configPath := "", execution.Options["config"].StringValue()
	if configPath == "" {
		directory = execution.WorkingDirectory
	}
	config, err := manager.Read(directory, configPath)
	if err != nil {
		return err
	}
	if operation, err = hsync.PushWithConfig(
		execution.Context,
		config,
		execution.Options["environment"].StringValue(),
		splitPatterns(execution.Options["providers"].StringValue()),
		nil,
	); err != nil {
		return err
	} else if operation.Status == hsync.StatusNoop || operation.Status == hsync.StatusAdopted {
		_, err = fmt.Fprintln(execution.Stdout, "Everything up-to-date")
	}
	return err
}

func splitPatterns(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}
