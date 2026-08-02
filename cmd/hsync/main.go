package main

import (
	"context"
	"fmt"
	"net/url"
	"os"

	cli "github.com/herdworks/cli-go"
	"github.com/herdworks/hsync"
)

var HSync = &cli.CLI{
	Name:        "hsync",
	Version:     "v0.2.0",
	Description: "hsync is an HTTP sync tool.",
	Commands: cli.Commands{
		downloadCommand(),
		initCommand(),
		pullCommand(),
		pushCommand(),
		resetCommand(),
		statusCommand(),
	},
	Options: cli.CommandOptions{
		{
			Name:        "help",
			ShortName:   "h",
			Description: "Display help information.",
			Boolean:     true,
			Secret:      true,
			Global:      true,
		},
	},
}

func main() {
	HSync.Execute()
}

func downloadCommand() *cli.Command {
	return &cli.Command{
		Config: cli.CommandConfig{
			Name:        "download",
			Description: "Download a website.",
			Usage:       "hsync download <url> <directory>",
			Validator: func(cmd *cli.Command) error {
				if len(cmd.Args) != 2 {
					return fmt.Errorf("download requires a URL and destination directory")
				}
				return nil
			},
			Handler: download,
		},
	}
}

func download(cmd *cli.Command) error {
	source, err := parseHTTPURL(cmd.Args[0])
	if err != nil {
		return fmt.Errorf("invalid URL %q", cmd.Args[0])
	}
	result, err := hsync.DownloadWithOptions(context.Background(), source, cmd.Args[1], hsync.DownloadOptions{Output: os.Stdout})
	if err != nil {
		return err
	}
	fmt.Println(result.ManifestID)
	return nil
}

func initCommand() *cli.Command {
	return &cli.Command{
		Config: cli.CommandConfig{
			Name:        "init",
			Description: "Initialize a website for synchronization.",
			Usage:       "hsync init <remote> [--config hsync.yaml]",
			Validator: func(cmd *cli.Command) error {
				if len(cmd.Args) != 1 {
					return fmt.Errorf("init requires one remote URL")
				}
				return nil
			},
			Handler: initialize,
		},
		Options: cli.CommandOptions{
			{Name: "config", ShortName: "c", Description: "Configuration file to create.", Default: hsync.DefaultConfigPath},
		},
	}
}

func initialize(cmd *cli.Command) error {
	return hsync.InitializeConfig(cmd.Options.Get("config"), cmd.Args[0])
}

func pullCommand() *cli.Command {
	return &cli.Command{
		Config: cli.CommandConfig{
			Name:        "pull",
			Description: "Pull remote changes into the workspace.",
			Usage:       "hsync pull [directory] [--config path]",
			Validator:   atMostOneDirectory,
			Handler: func(cmd *cli.Command) error {
				directory := commandDirectory(cmd)
				config, err := loadSyncConfig(directory, cmd.Options.Get("config"))
				if err != nil {
					return err
				}
				result, err := hsync.PullWithOptions(context.Background(), directory, config, hsync.PullOptions{Output: os.Stdout})
				if err != nil {
					return err
				}
				fmt.Println(result.ManifestID)
				return nil
			},
		}, Options: cli.CommandOptions{{Name: "config", ShortName: "c", Description: "Configuration path (or HSYNC_CONFIG / detected hsync config)."}},
	}
}

func pushCommand() *cli.Command {
	return &cli.Command{
		Config: cli.CommandConfig{
			Name:        "push",
			Description: "Push local changes to the remote.",
			Usage:       "hsync push <directory> [--config path] [options]",
			Validator: func(cmd *cli.Command) error {
				if len(cmd.Args) != 1 {
					return fmt.Errorf("push requires exactly one checkout directory")
				}
				if cmd.Options.Get("environment") != "" && cmd.Options.Get("providers") != "" {
					return fmt.Errorf("--providers and --environment are mutually exclusive")
				}
				return nil
			},
			Handler: func(cmd *cli.Command) error {
				configPath, err := hsync.ConfigPathAt(cmd.Args[0], cmd.Options.Get("config"))
				if err != nil {
					return err
				}
				return hsync.Push(context.Background(), cmd.Args[0], hsync.PushOptions{
					ConfigPath:  configPath,
					Environment: cmd.Options.Get("environment"),
					Providers:   hsync.SplitPatterns(cmd.Options.Get("providers")),
				})
			},
		},
		Options: cli.CommandOptions{
			{Name: "config", ShortName: "c", Description: "Configuration path (or HSYNC_CONFIG / detected hsync config)."},
			{Name: "environment", ShortName: "e", Description: "Deployment environment."},
			{Name: "providers", Description: "Comma-separated provider names."},
		},
	}
}

func resetCommand() *cli.Command {
	return &cli.Command{
		Config: cli.CommandConfig{
			Name:        "reset",
			Description: "Reset modified files in the workspace.",
			Usage:       "hsync reset [directory] [--config path] [--interactive]",
			Validator:   atMostOneDirectory,
			Handler: func(cmd *cli.Command) error {
				directory := commandDirectory(cmd)
				config, err := loadSyncConfig(directory, cmd.Options.Get("config"))
				if err != nil {
					return err
				}
				repository, remote, err := hsync.OpenCheckout(directory, config, nil)
				if err != nil {
					return err
				}
				result, err := repository.Reset(context.Background(), remote, hsync.ResetOptions{Output: os.Stdout, Interactive: cmd.Options.Get("interactive") == "true"})
				if err != nil {
					return err
				}
				fmt.Println(result.ManifestID)
				return nil
			},
		},
		Options: cli.CommandOptions{
			{Name: "config", ShortName: "c", Description: "Configuration path (or HSYNC_CONFIG / detected hsync config)."},
			{Name: "interactive", ShortName: "i", Description: "Choose each tracked file to restore.", Boolean: true},
		},
	}
}

func statusCommand() *cli.Command {
	return &cli.Command{
		Config: cli.CommandConfig{
			Name:        "status",
			Description: "List modified files in the workspace.",
			Usage:       "hsync status [directory] [--config path]",
			Validator:   atMostOneDirectory,
			Handler: func(cmd *cli.Command) error {
				directory := commandDirectory(cmd)
				config, err := loadSyncConfig(directory, cmd.Options.Get("config"))
				if err != nil {
					return err
				}
				repository, err := hsync.OpenRepository(directory)
				if err != nil {
					return err
				}
				repository.Config = &config
				result, err := repository.Status()
				if err != nil {
					return err
				}
				for _, change := range result.Changes {
					prefix := map[hsync.ChangeKind]string{
						hsync.ChangeAdded:    "A",
						hsync.ChangeModified: "M",
						hsync.ChangeDeleted:  "D",
					}[change.Kind]
					fmt.Printf("%s %s\n", prefix, change.Path)
				}
				return nil
			},
		}, Options: cli.CommandOptions{{Name: "config", ShortName: "c", Description: "Configuration path (or HSYNC_CONFIG / detected hsync config)."}},
	}
}

func loadSyncConfig(root, explicit string) (hsync.SyncConfig, error) {
	path, err := hsync.ConfigPathAt(root, explicit)
	if err != nil {
		return hsync.SyncConfig{}, err
	}
	if path == "" {
		return hsync.SyncConfig{}, fmt.Errorf("hsync configuration is required; use --config, HSYNC_CONFIG, or add hsync.yaml, hsync.json, .hsync.yaml, or .hsync.json at the repository root")
	}
	return hsync.LoadSyncConfig(path)
}

func atMostOneDirectory(cmd *cli.Command) error {
	if len(cmd.Args) > 1 {
		return fmt.Errorf("%s accepts at most one directory", cmd.Config.Name)
	}
	return nil
}

func commandDirectory(cmd *cli.Command) string {
	if len(cmd.Args) == 1 {
		return cmd.Args[0]
	}
	return "."
}

func parseHTTPURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid HTTP URL")
	}
	return parsed, nil
}
