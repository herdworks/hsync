package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/herdworks/clic"
	"github.com/herdworks/hsync"
)

type recordingConfigManager struct {
	config      hsync.RepositoryConfig
	err         error
	directories []string
	filenames   []string
	directory   string
	filename    string
	remote      string
	verbose     bool
}

func (*recordingConfigManager) DefaultConfigPath() string { return "site.yaml" }

func (manager *recordingConfigManager) Read(directory, filename string) (hsync.RepositoryConfig, error) {
	manager.directories = append(manager.directories, directory)
	manager.filenames = append(manager.filenames, filename)
	return manager.config, manager.err
}

func (manager *recordingConfigManager) Initialize(directory, filename, remote string, verbose bool) error {
	manager.directory = directory
	manager.filename = filename
	manager.remote = remote
	manager.verbose = verbose
	return nil
}

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

	first, second := InitCommand(nil), InitCommand(nil)
	if first.Spec.Options["config"] == second.Spec.Options["config"] {
		t.Fatal("factory returned shared options")
	}
	remixed := InitCommand(nil)
	remixed.Meta.Name = "bootstrap"
	remixed.Meta.Description = "Host-specific setup."
	remixed.Spec.Options["config"].Meta.Default = filepath.Join(t.TempDir(), "remixed.yaml")
	host := clic.CLI{Command: clic.Command{Spec: clic.CommandSpec{Key: "host"}, Commands: clic.Commands{"init": &remixed}}}
	result = host.Run(execution(t), clic.Arguments{"bootstrap", "https://example.com/.sync/index.json"})
	if result.Error != nil {
		t.Fatalf("remixed Run() = %#v", result)
	}
	if _, err := os.Stat(remixed.Spec.Options["config"].Meta.Default.(string)); err != nil {
		t.Fatalf("remixed config = %v", err)
	}
}

func TestCommandUsesConfiguredConfigManager(t *testing.T) {
	manager := &recordingConfigManager{}
	command := Command(manager)
	composition := clic.CLI{Command: command}
	workspace := t.TempDir()

	result := composition.Run(executionAt(t, workspace), clic.Arguments{"init", "https://example.com", "--config", "custom.yaml"})
	if result.Error != nil {
		t.Fatalf("init Run() = %v", result.Error)
	}
	if manager.directory != workspace || manager.filename != "custom.yaml" || manager.remote != "https://example.com" {
		t.Fatalf("manager = %#v", manager)
	}

	for _, test := range []struct {
		arguments clic.Arguments
		contains  string
	}{
		{arguments: clic.Arguments{"pull", "--config", "custom.yaml"}, contains: "pull requires sync configuration"},
		{arguments: clic.Arguments{"push", "--config", "custom.yaml"}, contains: "push requires sync configuration"},
		{arguments: clic.Arguments{"remote", "--config", "custom.yaml"}, contains: "sync configuration is empty"},
		{arguments: clic.Arguments{"reset", "--config", "custom.yaml"}, contains: "reset requires sync configuration"},
		{arguments: clic.Arguments{"status", "--config", "custom.yaml"}, contains: "status requires sync configuration"},
	} {
		t.Run(test.arguments[0], func(t *testing.T) {
			result := composition.Run(executionAt(t, workspace), test.arguments)
			if result.Error == nil || !strings.Contains(result.Error.Error(), test.contains) {
				t.Fatalf("Run(%v) error = %v, want containing %q", test.arguments, result.Error, test.contains)
			}
		})
	}
	if len(manager.directories) != 5 {
		t.Fatalf("reader calls = %#v", manager)
	}
	for index := range manager.directories {
		if manager.directories[index] != "" || manager.filenames[index] != "custom.yaml" {
			t.Fatalf("reader call %d = (%q, %q)", index, manager.directories[index], manager.filenames[index])
		}
	}
}

func TestInitCommandRequestsVerboseConfiguration(t *testing.T) {
	manager := &recordingConfigManager{}
	composition := clic.CLI{Command: Command(manager)}
	result := composition.Run(execution(t), clic.Arguments{"init", "https://example.com", "--verbose"})
	if result.Error != nil {
		t.Fatalf("Run(init --verbose) error = %v", result.Error)
	}
	if !manager.verbose {
		t.Fatal("Initialize() verbose = false, want true")
	}
}

func TestGlobalConfigOptionSupportsInitAndExistingCheckouts(t *testing.T) {
	global := GlobalConfigOption()
	if global.Meta.Default != nil {
		t.Fatalf("GlobalConfigOption().Meta.Default = %#v, want nil", global.Meta.Default)
	}
	if global.Meta.EnvName != "{{ .EnvPrefix }}CONFIG" {
		t.Fatalf("GlobalConfigOption().Meta.EnvName = %q", global.Meta.EnvName)
	}

	init := InitCommand(nil).Spec.Options["config"]
	if init.Meta.Default != hsync.DefaultConfigPath {
		t.Fatalf("InitCommand config default = %#v, want %q", init.Meta.Default, hsync.DefaultConfigPath)
	}
	if init.Meta.Description != "Configuration file to create (or ${{ .EnvName }})." {
		t.Fatalf("InitCommand config description = %q", init.Meta.Description)
	}
	if init.Meta.ShowDefault {
		t.Fatal("InitCommand config should preserve its hidden default")
	}

	for _, command := range []clic.Command{PullCommand(nil), PushCommand(nil), RemoteCommand(nil), ResetCommand(nil), StatusCommand(nil)} {
		config := command.Spec.Options["config"]
		if config.Meta.Default != nil || config.Meta.Description != global.Meta.Description || config.Meta.EnvName != global.Meta.EnvName {
			t.Fatalf("%s config = %#v, want reusable global config option", command.Spec.Key, config.Meta)
		}
	}
}

func TestCommandFactorySupportsRootRemixing(t *testing.T) {
	first, second := Command(nil), Command(nil)
	if first.Commands["init"] == second.Commands["init"] {
		t.Fatal("factory returned shared commands")
	}

	remixed := Command(nil)
	remixed.Meta.EnvPrefix = ""
	remixed.Spec.Key = "sync"
	host := clic.CLI{
		Command: clic.Command{
			Meta: clic.CommandMeta{EnvPrefix: "HYPER_"},
			Spec: clic.CommandSpec{
				Key: "hyperctl",
			},
			Commands: clic.Commands{
				"sync": &remixed,
			},
		},
	}
	if mounted, ok := host.Command.SubCommand("sync"); !ok || mounted != &remixed {
		t.Fatalf("host sync command = %#v, %t", mounted, ok)
	}
	config := remixed.Commands["pull"].Spec.Options["config"]
	if config.Meta.EnvName != "{{ .EnvPrefix }}CONFIG" {
		t.Fatalf("config environment template = %q", config.Meta.EnvName)
	}
}

func TestCommandFactoriesComposeEveryCanonicalHsyncCommand(t *testing.T) {
	factories := []func() clic.Command{
		DownloadCommand,
		func() clic.Command { return InitCommand(nil) },
		func() clic.Command { return PullCommand(nil) },
		func() clic.Command { return PushCommand(nil) },
		func() clic.Command { return RemoteCommand(nil) },
		func() clic.Command { return ResetCommand(nil) },
		func() clic.Command { return StatusCommand(nil) },
	}
	expected := []string{"download", "init", "pull", "push", "remote", "reset", "status"}
	commands := clic.MustCommandsFrom(factories...)
	composition := clic.CLI{Command: clic.Command{
		Spec:     clic.CommandSpec{Key: "host"},
		Commands: commands,
	}}
	if err := composition.Validate(); err != nil {
		t.Fatalf("factory composition Validate() = %v", err)
	}
	for index, key := range expected {
		command, ok := CLI.SubCommand(key)
		if !ok || command.Spec.Key != key || command.Name() != key {
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

func TestRemoteCommandPrintsConfiguredAndResolvedRemote(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "hsync.yaml"), []byte("sync:\n  remote: https://example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		arguments clic.Arguments
		want      string
	}{
		{arguments: clic.Arguments{"remote"}, want: "https://example.com\n"},
		{arguments: clic.Arguments{"remote", "--index-url"}, want: "https://example.com/.hsync/index.json\n"},
	} {
		t.Run(strings.Join(test.arguments, " "), func(t *testing.T) {
			output := &bytes.Buffer{}
			execution := execution(t)
			execution.WorkingDirectory = workspace
			execution.Stdout = output
			result := CLI.Run(execution, test.arguments)
			if result.Error != nil {
				t.Fatalf("CLI.Run(%v) error = %v", test.arguments, result.Error)
			}
			if output.String() != test.want {
				t.Fatalf("CLI.Run(%v) output = %q, want %q", test.arguments, output.String(), test.want)
			}
		})
	}
}

func TestStatusCommandPrintsChangesAndSummary(t *testing.T) {
	workspace := t.TempDir()
	base, err := hsync.NewSealedManifest("/.hsync/index.json", "", []hsync.ManifestEntry{
		{Path: "deleted.txt", Blob: hsync.NewIdentifier([]byte("deleted")), Size: 7, Mimetype: "text/plain"},
		{Path: "modified.txt", Blob: hsync.NewIdentifier([]byte("before")), Size: 6, Mimetype: "text/plain"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, ".hsync"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, hsync.LocalManifestPath), base.Raw(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "hsync.yaml"), []byte("sync:\n  remote: https://example.com\n  include:\n    paths: [added.txt, deleted.txt, modified.txt]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{"added.txt": "added", "modified.txt": "after"} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	output := &bytes.Buffer{}
	execution := executionAt(t, workspace)
	execution.Stdout = output
	result := CLI.Run(execution, clic.Arguments{"status"})
	if result.Error != nil {
		t.Fatalf("CLI.Run(status) error = %v", result.Error)
	}
	want := "     added added.txt\n   deleted deleted.txt\n  modified modified.txt\n\n1 added, 1 modified, 1 deleted\n"
	if output.String() != want {
		t.Fatalf("CLI.Run(status) output = %q, want %q", output.String(), want)
	}
}

func TestStatusCommandVerbosePrintsIgnoredAndLimitedFiles(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{
		"content/a-include.txt": "yes",
		"content/b-ignore.txt":  "ignored",
		"content/c-skip.txt":    "skip!",
		"outside.txt":           "outside",
	} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	config := "sync:\n  remote: https://example.com\n  max_file_bytes: 4\n  include:\n    paths: [content/**]\n  ignore:\n    paths: [content/b-ignore.txt]\n"
	if err := os.WriteFile(filepath.Join(workspace, "hsync.yaml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	output := &bytes.Buffer{}
	execution := executionAt(t, workspace)
	execution.Stdout = output
	result := CLI.Run(execution, clic.Arguments{"status", "--verbose"})
	if result.Error != nil {
		t.Fatalf("CLI.Run(status --verbose) error = %v", result.Error)
	}
	want := "     added content/a-include.txt\n   ignored content/b-ignore.txt\n   limited content/c-skip.txt\n\n1 added, 0 modified, 1 ignored, 1 limited, 0 deleted\n"
	if output.String() != want {
		t.Fatalf("CLI.Run(status --verbose) output = %q, want %q", output.String(), want)
	}
}

func TestNoopCommandsReportGitStyleStatus(t *testing.T) {
	workspace := t.TempDir()
	manifest, err := hsync.NewSealedManifest("/.hsync/index.json", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, ".hsync"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, hsync.LocalManifestPath), manifest.Raw(), 0o644); err != nil {
		t.Fatal(err)
	}
	manifestFilename, err := manifest.Filename()
	if err != nil {
		t.Fatal(err)
	}
	index, err := json.Marshal(hsync.Index{Version: 1, UpdatedAt: time.Now().UTC(), Published: manifest.ID(), History: []hsync.Identifier{manifest.ID()}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/.hsync/index.json":
			_, _ = writer.Write(index)
		case "/.hsync/manifests/" + manifestFilename:
			_, _ = writer.Write(manifest.Raw())
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	if err := os.WriteFile(filepath.Join(workspace, "hsync.yaml"), []byte("sync:\n  remote: "+server.URL+"/.hsync/index.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		arguments clic.Arguments
		want      string
	}{
		{arguments: clic.Arguments{"pull"}, want: "Already up to date.\n"},
		{arguments: clic.Arguments{"push"}, want: "Everything up-to-date\n"},
	}
	for _, test := range tests {
		output := &bytes.Buffer{}
		execution := execution(t)
		execution.WorkingDirectory = workspace
		execution.Stdout = output
		result := CLI.Run(execution, test.arguments)
		if result.Error != nil {
			t.Fatalf("CLI.Run(%v) error = %v", test.arguments, result.Error)
		}
		if output.String() != test.want {
			t.Fatalf("CLI.Run(%v) output = %q, want %q", test.arguments, output.String(), test.want)
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
		{name: "push positional", command: PushCommand(nil), args: clic.Arguments{"publish", "checkout"}, contains: "publish accepts no positional arguments; use -c/--config to select a repository"},
		{name: "push options", command: PushCommand(nil), args: clic.Arguments{"publish", "--environment", "production", "--targets", "origin"}, contains: "-p/--targets and -e/--environment are mutually exclusive"},
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
			cli := clic.CLI{Command: clic.Command{Spec: clic.CommandSpec{Key: "host"}, Commands: clic.Commands{command.Spec.Key: &command}}}
			result := cli.Run(execution(t), test.args)
			if result.Error == nil || !strings.Contains(result.Error.Error(), test.contains) {
				t.Fatalf("Run() error = %v, want containing %q", result.Error, test.contains)
			}
		})
	}
}

func TestCheckoutCommandsPassDirectoryAndConfigToManager(t *testing.T) {
	checkout := t.TempDir()
	manager := &recordingConfigManager{err: errors.New("config directory mismatch")}
	for _, test := range []struct {
		name    string
		command clic.Command
	}{
		{name: "pull", command: PullCommand(manager)},
		{name: "remote", command: RemoteCommand(manager)},
		{name: "reset", command: ResetCommand(manager)},
		{name: "status", command: StatusCommand(manager)},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := test.command
			composition := clic.CLI{Command: clic.Command{Spec: clic.CommandSpec{Key: "host"}, Commands: clic.Commands{command.Spec.Key: &command}}}
			result := composition.Run(executionAt(t, checkout), clic.Arguments{test.name, checkout, "--config", "site.yaml"})
			if result.Error == nil || !strings.Contains(result.Error.Error(), "config directory mismatch") {
				t.Fatalf("Run() error = %v, want manager error", result.Error)
			}
		})
	}
	for index := range manager.directories {
		if manager.directories[index] != checkout || manager.filenames[index] != "site.yaml" {
			t.Fatalf("manager read %d = (%q, %q)", index, manager.directories[index], manager.filenames[index])
		}
	}
}

func TestCheckoutCommandsAcceptMatchingDirectoryAndConfig(t *testing.T) {
	checkout := t.TempDir()
	manager := &recordingConfigManager{config: hsync.RepositoryConfig{Path: filepath.Join(checkout, "site.yaml")}}
	command := PullCommand(manager)
	composition := clic.CLI{Command: clic.Command{Spec: clic.CommandSpec{Key: "host"}, Commands: clic.Commands{"pull": &command}}}
	result := composition.Run(executionAt(t, checkout), clic.Arguments{"pull", checkout, "--config", "site.yaml"})
	if result.Error == nil || strings.Contains(result.Error.Error(), "belongs to") {
		t.Fatalf("Run() error = %v, want a non-directory error", result.Error)
	}
	if len(manager.directories) != 1 || manager.directories[0] != checkout || manager.filenames[0] != "site.yaml" {
		t.Fatalf("manager reads = %#v", manager)
	}
}

func execution(t *testing.T) clic.Execution {
	t.Helper()
	return executionAt(t, t.TempDir())
}

func executionAt(t *testing.T, directory string) clic.Execution {
	t.Helper()
	return clic.Execution{Context: context.Background(), Stdout: io.Discard, Stderr: io.Discard, LookupEnv: func(string) (string, bool) { return "", false }, WorkingDirectory: directory}
}
