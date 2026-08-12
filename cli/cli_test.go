package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/herdworks/clic"
)

func TestCLIAndCommandFactoriesSupportCanonicalAndRemixedMounting(t *testing.T) {
	if err := CLI.Validate(); err != nil {
		t.Fatalf("canonical Validate() = %v", err)
	}
	canonicalPath := filepath.Join(t.TempDir(), "canonical.yaml")
	result := CLI.Run(execution(t), clic.Arguments{"init", "https://example.com/.sync/index.json", "--config", canonicalPath})
	if result.Error != nil {
		t.Fatalf("canonical Run() = %#v", result)
	}
	if _, err := os.Stat(canonicalPath); err != nil {
		t.Fatalf("canonical config = %v", err)
	}

	first, second := InitCommand(), InitCommand()
	if first.Spec.Options["config"] == second.Spec.Options["config"] {
		t.Fatal("factory returned shared options")
	}
	remixed := InitCommand()
	remixed.Meta.Name = "bootstrap"
	remixed.Meta.Description = "Host-specific setup."
	remixed.Spec.Options["config"].Meta.Default = filepath.Join(t.TempDir(), "remixed.yaml")
	host := clic.CLI{Command: clic.Command{Meta: clic.CommandMeta{Name: "host"}, Spec: clic.CommandSpec{Key: "host"}, Commands: clic.Commands{"init": &remixed}}}
	result = host.Run(execution(t), clic.Arguments{"bootstrap", "https://example.com/.sync/index.json"})
	if result.Error != nil {
		t.Fatalf("remixed Run() = %#v", result)
	}
	if _, err := os.Stat(remixed.Spec.Options["config"].Meta.Default.(string)); err != nil {
		t.Fatalf("remixed config = %v", err)
	}
}

func TestCommandFactoriesComposeEveryCanonicalHsyncCommand(t *testing.T) {
	factories := []func() clic.Command{
		DownloadCommand,
		InitCommand,
		PullCommand,
		PushCommand,
		ResetCommand,
		StatusCommand,
	}
	expected := []string{"download", "init", "pull", "push", "reset", "status"}
	commands := clic.MustCommandsFrom(factories...)
	composition := clic.CLI{Command: clic.Command{
		Meta:     clic.CommandMeta{Name: "host"},
		Spec:     clic.CommandSpec{Key: "host"},
		Commands: commands,
	}}
	if err := composition.Validate(); err != nil {
		t.Fatalf("factory composition Validate() = %v", err)
	}
	for index, key := range expected {
		command, ok := CLI.SubCommand(key)
		if !ok || command.Spec.Key != key || command.Meta.Name != key {
			t.Fatalf("canonical command %q = %#v, %t", key, command, ok)
		}
		if command.Spec.Help == nil {
			t.Fatalf("canonical command %q does not configure help", key)
		}
		first, second := factories[index](), factories[index]()
		for optionKey, option := range first.Spec.Options {
			if option == second.Spec.Options[optionKey] {
				t.Fatalf("%s factory shared option %q", key, optionKey)
			}
		}
	}
}

func TestLeafCommandHelp(t *testing.T) {
	output := &bytes.Buffer{}
	execution := execution(t)
	execution.Stdout = output
	result := CLI.Run(execution, clic.Arguments{"init", "--help"})
	if result.Error != nil {
		t.Fatalf("Run() = %#v", result)
	}
	if !strings.Contains(output.String(), "hsync init <remote>") || !strings.Contains(output.String(), "-h, --help") {
		t.Fatalf("help output = %q", output.String())
	}
}

func TestRemixedValidatorsUseHostMetadata(t *testing.T) {
	tests := []struct {
		name     string
		command  clic.Command
		args     clic.Arguments
		contains string
	}{
		{name: "pull", command: PullCommand(), args: clic.Arguments{"retrieve", "checkout", "--config", "site.yaml"}, contains: "retrieve cannot combine a directory argument with -c/--config or $HYPER_CONFIG"},
		{name: "reset", command: ResetCommand(), args: clic.Arguments{"restore", "checkout", "--config", "site.yaml"}, contains: "restore cannot combine a directory argument with -c/--config or $HYPER_CONFIG"},
		{name: "status", command: StatusCommand(), args: clic.Arguments{"inspect", "checkout", "--config", "site.yaml"}, contains: "inspect cannot combine a directory argument with -c/--config or $HYPER_CONFIG"},
		{name: "push positional", command: PushCommand(), args: clic.Arguments{"publish", "checkout"}, contains: "publish accepts no positional arguments; use -c/--config to select a repository"},
		{name: "push options", command: PushCommand(), args: clic.Arguments{"publish", "--environment", "production", "--targets", "origin"}, contains: "-p/--targets and -e/--environment are mutually exclusive"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := test.command
			command.Meta.Name = map[string]string{"pull": "retrieve", "reset": "restore", "status": "inspect", "push": "publish"}[command.Spec.Key]
			if config, ok := command.Spec.Options["config"]; ok {
				config.Meta.EnvName = "HYPER_CONFIG"
			}
			if providers, ok := command.Spec.Options["providers"]; ok {
				providers.Meta.Name = "targets"
			}
			cli := clic.CLI{Command: clic.Command{Meta: clic.CommandMeta{Name: "host"}, Spec: clic.CommandSpec{Key: "host"}, Commands: clic.Commands{command.Spec.Key: &command}}}
			result := cli.Run(execution(t), test.args)
			if result.Error == nil || !strings.Contains(result.Error.Error(), test.contains) {
				t.Fatalf("Run() error = %v, want containing %q", result.Error, test.contains)
			}
		})
	}
}

func execution(t *testing.T) clic.Execution {
	t.Helper()
	return clic.Execution{Context: context.Background(), Stdout: io.Discard, Stderr: io.Discard, LookupEnv: func(string) (string, bool) { return "", false }, WorkingDirectory: t.TempDir()}
}
