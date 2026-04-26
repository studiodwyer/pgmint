package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"time"
)

func runList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	addr := fs.String("addr", Defaults.DaemonAddr, "daemon address")
	fs.Parse(args)

	client := &http.Client{Timeout: 30 * time.Second}

	resp, err := client.Get("http://" + *addr + "/clone")
	if err != nil {
		return fmt.Errorf("failed to list clones: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("list failed (status %d): %s", resp.StatusCode, body)
	}

	var result struct {
		Clones []string `json:"clones"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if len(result.Clones) == 0 {
		fmt.Println("no active clones")
		return nil
	}

	for _, c := range result.Clones {
		fmt.Println(c)
	}
	return nil
}
