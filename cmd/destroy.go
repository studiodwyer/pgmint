package cmd

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"time"
)

func runDestroy(args []string) error {
	fs := flag.NewFlagSet("destroy", flag.ExitOnError)
	addr := fs.String("addr", Defaults.DaemonAddr, "daemon address")
	removeOrphans := fs.Bool("remove-orphans", false, "also destroy all clones forked from this clone")
	fs.Parse(args)

	if fs.NArg() != 1 {
		return fmt.Errorf("usage: pgmint destroy <clone-name>")
	}
	cloneName := fs.Arg(0)

	client := &http.Client{Timeout: 60 * time.Second}

	url := "http://" + *addr + "/clone/" + cloneName
	if *removeOrphans {
		url += "?remove-orphans=true"
	}

	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to destroy clone: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("destroy failed (status %d): %s", resp.StatusCode, body)
	}

	fmt.Printf("clone %q destroyed\n", cloneName)
	return nil
}
