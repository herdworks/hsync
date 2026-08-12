package cli

import (
	"fmt"
	"net/url"

	"github.com/herdworks/clic"
	"github.com/herdworks/hsync"
)

// download command
func DownloadCommand() clic.Command {
	return clic.Command{
		Meta: DownloadMeta(),
		Spec: DownloadSpec(),
	}
}

func DownloadMeta() clic.CommandMeta {
	return clic.CommandMeta{
		Name:        "download",
		Description: "Download a website.",
		Usage:       []string{"hsync download <url> <directory>"},
	}
}

func DownloadSpec() clic.CommandSpec {
	return clic.CommandSpec{
		Key:       "download",
		Help:      clic.DefaultHelp,
		Validator: downloadValidator,
		Handler:   downloadHandler,
	}
}

// download validation
func downloadValidator(execution *clic.Execution) error {
	if len(execution.Command.State.Arguments) != 2 {
		return fmt.Errorf("download requires a URL and destination directory")
	}
	return nil
}

// download handler
func downloadHandler(execution *clic.Execution) error {
	source, err := url.Parse(execution.Command.State.Arguments[0])
	if err != nil || source.Scheme == "" || source.Host == "" {
		return fmt.Errorf("invalid URL %q", execution.Command.State.Arguments[0])
	}
	result, err := hsync.DownloadWithOptions(execution.Context, source, execution.Command.State.Arguments[1], hsync.DownloadOptions{Output: execution.Stdout})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(execution.Stdout, result.ManifestID)
	return err
}
