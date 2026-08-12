package cli

import (
	"fmt"

	"github.com/herdworks/clic"
	"github.com/herdworks/hsync"
)

// init command
func InitCommand() clic.Command {
	return clic.Command{
		Meta: InitMeta(),
		Spec: InitSpec(),
	}
}

func InitMeta() clic.CommandMeta {
	return clic.CommandMeta{
		Name:        "init",
		Description: "Initialize a website for synchronization.",
		Usage:       []string{"hsync init <remote> [--config hsync.yaml]"},
	}
}

func InitSpec() clic.CommandSpec {
	config := InitConfigOption()
	return clic.CommandSpec{
		Key:       "init",
		Options:   clic.Options{"config": &config},
		Help:      clic.DefaultHelp,
		Validator: initValidator,
		Handler:   initHandler,
	}
}

// init --config option
func InitConfigOption() clic.Option {
	return clic.Option{
		Meta: InitConfigMeta(),
		Spec: InitConfigSpec(),
	}
}

func InitConfigMeta() clic.OptionMeta {
	return clic.OptionMeta{
		Name:        "config",
		ShortName:   "c",
		Description: "Configuration file to create.",
		Default:     hsync.DefaultConfigPath,
	}
}

func InitConfigSpec() clic.OptionSpec {
	return clic.OptionSpec{
		Key:  "config",
		Kind: clic.StringOption,
	}
}

// init validation
func initValidator(execution *clic.Execution) error {
	if len(execution.Command.State.Arguments) != 1 {
		return fmt.Errorf("init requires one remote URL")
	}
	return nil
}

// init handler
func initHandler(execution *clic.Execution) error {
	return hsync.InitializeConfig(execution.Options["config"].StringValue(), execution.Command.State.Arguments[0])
}
