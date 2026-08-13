package cli

import (
	"fmt"

	"github.com/herdworks/clic"
	"github.com/herdworks/hsync"
)

// push command
func PushCommand() clic.Command {
	config := GlobalConfigOption()
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
				"config":      &config,
				"environment": &environment,
				"providers":   &providers,
			},
			Validator: pushValidator,
			Handler:   pushHandler,
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
func pushHandler(execution *clic.Execution) error {
	filename := execution.Options["config"].StringValue()
	if filename == "" {
		var err error
		filename, err = hsync.DiscoverConfig(execution.WorkingDirectory)
		if err != nil {
			return err
		}
	}
	return hsync.Push(execution.Context, hsync.PushOptions{ConfigPath: filename, Environment: execution.Options["environment"].StringValue(), Providers: hsync.SplitPatterns(execution.Options["providers"].StringValue())})
}
