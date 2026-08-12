package cli

import (
	"fmt"

	"github.com/herdworks/clic"
	"github.com/herdworks/hsync"
)

// push command
func PushCommand() clic.Command {
	return clic.Command{
		Meta: PushMeta(),
		Spec: PushSpec(),
	}
}
func PushMeta() clic.CommandMeta {
	return clic.CommandMeta{
		Name:        "push",
		Description: "Push local changes to the remote.",
		Usage: []string{
			"hsync push",
			"hsync push [--config path]",
			"hsync push [--config path] [--environment environment]",
			"hsync push [--config path] [--providers provider,provider]",
		},
	}
}
func PushSpec() clic.CommandSpec {
	var config clic.Option = PushConfigOption()
	var environment clic.Option = PushEnvironmentOption()
	var providers clic.Option = PushProvidersOption()
	return clic.CommandSpec{
		Key:  "push",
		Help: clic.DefaultHelp,
		Options: clic.Options{
			"config":      &config,
			"environment": &environment,
			"providers":   &providers,
		},
		Validator: pushValidator,
		Handler:   pushHandler,
	}
}

// push --config option
func PushConfigOption() clic.Option {
	return clic.Option{
		Meta: PushConfigMeta(),
		Spec: PushConfigSpec(),
	}
}
func PushConfigMeta() clic.OptionMeta {
	return configOptionMeta()
}
func PushConfigSpec() clic.OptionSpec {
	return clic.OptionSpec{
		Key:  "config",
		Kind: clic.StringOption,
	}
}

// push --environment option
func PushEnvironmentOption() clic.Option {
	return clic.Option{
		Meta: PushEnvironmentMeta(),
		Spec: PushEnvironmentSpec(),
	}
}
func PushEnvironmentMeta() clic.OptionMeta {
	return clic.OptionMeta{
		Name:        "environment",
		ShortName:   "e",
		Description: "Deployment environment (default: hsync).",
	}
}
func PushEnvironmentSpec() clic.OptionSpec {
	return clic.OptionSpec{
		Key:  "environment",
		Kind: clic.StringOption,
	}
}

// push --providers option
func PushProvidersOption() clic.Option {
	return clic.Option{
		Meta: PushProvidersMeta(),
		Spec: PushProvidersSpec(),
	}
}
func PushProvidersMeta() clic.OptionMeta {
	return clic.OptionMeta{
		Name:        "providers",
		ShortName:   "p",
		Description: "Comma-separated provider names.",
	}
}
func PushProvidersSpec() clic.OptionSpec {
	return clic.OptionSpec{
		Key:  "providers",
		Kind: clic.StringOption,
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
