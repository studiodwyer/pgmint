package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

func runClone(args []string) error {
	fs := flag.NewFlagSet("clone", flag.ExitOnError)
	addr := fs.String("addr", Defaults.DaemonAddr, "daemon address")
	source := fs.String("source", "", "source database to clone from (default: source DB)")
	name := fs.String("name", "", "name for the cloned database (default: auto-generated)")
	fs.Parse(args)

	client := &http.Client{Timeout: 30 * time.Second}

	u := url.URL{
		Scheme: "http",
		Host:   *addr,
		Path:   "/clone",
	}
	if *source != "" {
		u.Path = "/clone/" + *source
	}
	q := u.Query()
	if *name != "" {
		q.Set("name", *name)
	}
	u.RawQuery = q.Encode()

	resp, err := client.Post(u.String(), "application/json", nil)
	if err != nil {
		return fmt.Errorf("failed to request clone: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("clone request failed (status %d): %s", resp.StatusCode, body)
	}

	var result struct {
		ConnectionString string `json:"connection_string"`
		CloneName        string `json:"clone_name"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	fmt.Println(result.ConnectionString)
	return nil
}
