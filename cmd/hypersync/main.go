package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/herdworks/hypersync-go/internal/doc"
	"github.com/herdworks/hypersync-go/internal/materialize"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "hypersync:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}
	switch args[0] {
	case "init":
		if len(args) != 2 {
			return fmt.Errorf("usage: hypersync init <path>")
		}
		return materialize.Init(args[1])
	case "scan":
		if len(args) != 2 {
			return fmt.Errorf("usage: hypersync scan <path>")
		}
		result, err := materialize.Scan(args[1])
		if err != nil {
			return err
		}
		fmt.Printf("documents_changed=%d\n", result.DocumentsChanged)
		fmt.Printf("ops_written=%d\n", result.OpsWritten)
		fmt.Printf("assets_stored=%d\n", result.AssetsStored)
		for _, warning := range result.Warnings {
			fmt.Printf("warning=%s\n", warning)
		}
		return nil
	case "status":
		if len(args) != 2 {
			return fmt.Errorf("usage: hypersync status <path>")
		}
		status, err := materialize.LoadStatus(args[1])
		if err != nil {
			return err
		}
		fmt.Printf("actor_id=%s\n", status.ActorID)
		fmt.Printf("documents=%d\n", status.DocumentCount)
		fmt.Printf("assets=%d\n", status.AssetCount)
		fmt.Printf("conflicted_docs=%d\n", len(status.ConflictedDocs))
		docIDs := make([]string, 0, len(status.Heads))
		for docID := range status.Heads {
			docIDs = append(docIDs, docID)
		}
		sort.Strings(docIDs)
		for _, docID := range docIDs {
			fmt.Printf("heads.%s=%s\n", docID, formatHeads(status.Heads[docID]))
		}
		return nil
	case "materialize":
		if len(args) != 2 {
			return fmt.Errorf("usage: hypersync materialize <path>")
		}
		return materialize.Materialize(args[1])
	case "log":
		if len(args) != 3 {
			return fmt.Errorf("usage: hypersync log <path> <docID>")
		}
		ops, err := materialize.LoadLog(args[1], args[2])
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		for _, op := range ops {
			if err := enc.Encode(op); err != nil {
				return err
			}
		}
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: hypersync <init|scan|status|materialize|log> ...")
}

func formatHeads(heads []doc.OpID) string {
	parts := make([]string, len(heads))
	for i, head := range heads {
		parts[i] = head.String()
	}
	return strings.Join(parts, ",")
}
