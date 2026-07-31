package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/herdworks/hypersync-go"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "init":
		initCommand(os.Args[2:])
	case "reconcile":
		reconcileCommand(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: hypersync <init|reconcile> [options]")
	fmt.Fprintln(os.Stderr, "  init --manifest <file> --sync-path <absolute path> --include <glob,...> [--ignore <glob,...>] [--max-file-size <integer><unit>]")
	fmt.Fprintln(os.Stderr, "  reconcile --source <dir> --manifest <file> --build <dir> --base-url <https://site.example/>")
}

func initCommand(arguments []string) {
	flags := flag.NewFlagSet("init", flag.ExitOnError)
	manifest := flags.String("manifest", "", "persistent local manifest file")
	syncPath := flags.String("sync-path", "", "absolute deployed synchronization path")
	include := flags.String("include", "", "comma-separated include path globs")
	ignore := flags.String("ignore", "", "comma-separated ignore path globs")
	maxFileSize := flags.String("max-file-size", "", "maximum managed file size, such as 5MB")
	_ = flags.Parse(arguments)
	if *manifest == "" || *syncPath == "" || *include == "" {
		flags.Usage()
		os.Exit(2)
	}
	config := hypersync.ScanConfig{Include: hypersync.Rules{Paths: hypersync.SplitPatterns(*include)}, Ignore: hypersync.Rules{Paths: hypersync.SplitPatterns(*ignore)}}
	if *maxFileSize != "" {
		value, err := hypersync.ParseSize(*maxFileSize)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		config.MaxFileSize = value
	}
	repository := hypersync.Repository{ManifestPath: *manifest}
	if _, _, err := repository.Initialize(*syncPath, config); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func reconcileCommand(arguments []string) {
	flags := flag.NewFlagSet("reconcile", flag.ExitOnError)
	source := flags.String("source", "", "source root scanned by manifest configuration")
	manifestPath := flags.String("manifest", "", "persistent local manifest file")
	build := flags.String("build", "", "deployment build directory")
	baseURL := flags.String("base-url", "", "public HTTP base URL for live synchronization state")
	_ = flags.Parse(arguments)
	if *source == "" || *manifestPath == "" || *build == "" || *baseURL == "" {
		flags.Usage()
		os.Exit(2)
	}
	parsedURL, err := url.Parse(*baseURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		fmt.Fprintf(os.Stderr, "invalid --base-url %q\n", *baseURL)
		os.Exit(2)
	}
	repository := hypersync.Repository{Root: *source, ManifestPath: *manifestPath}
	_, _, manifest, err := repository.LoadManifest()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	sync := hypersync.Sync{Repository: repository, Payload: hypersync.Payload{Root: *build}, Remote: hypersync.Remote{BaseURL: parsedURL, SyncPath: manifest.SyncPath}, Now: time.Now}
	if err := sync.Prepare(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if sync.Changeset != nil {
		fmt.Fprintln(os.Stdout, sync.Changeset.ManifestID)
	}
}
