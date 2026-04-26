package cmd

import (
	"context"
	"flag"
	"fmt"

	"github.com/studiodwyer/pgmint/internal/docker"
)

func runConnection(args []string) error {
	fs := flag.NewFlagSet("connection", flag.ExitOnError)
	instanceName := fs.String("name", Defaults.Name, "instance name")
	fs.Parse(args)

	ctx := context.Background()

	mgr, err := docker.NewManager()
	if err != nil {
		return fmt.Errorf("failed to create docker manager: %w", err)
	}

	container, err := mgr.FindByName(ctx, *instanceName)
	if err != nil {
		return fmt.Errorf("failed to find container: %w", err)
	}
	if container == nil {
		return fmt.Errorf("container %q not found — run 'init' first", *instanceName)
	}

	fmt.Printf("postgres://postgres:%s@%s:%d/%s?sslmode=disable\n",
		container.Password, container.PgHost, container.Port, container.SourceDB)
	return nil
}
