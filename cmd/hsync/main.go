// Command hsync synchronizes a checkout with an HTTP-hosted website.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/herdworks/clic"
	"github.com/herdworks/hsync/cli"
)

func main() {
	var result clic.Result
	var execution = clic.Execution{
		Context:          context.Background(),
		Stdin:            os.Stdin,
		Stdout:           os.Stdout,
		Stderr:           os.Stderr,
		LookupEnv:        os.LookupEnv,
		WorkingDirectory: ".",
	}
	if result = cli.CLI.Run(execution, os.Args[1:]); result.Error != nil {
		fmt.Fprintln(os.Stderr, result.Error)
	}
	os.Exit(result.ExitCode)
}
