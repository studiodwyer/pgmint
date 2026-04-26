package cmd

import (
	"context"
	"flag"
	"fmt"
	"log/slog"

	"github.com/studiodwyer/pgmint/internal/docker"
)

func runTeardown(args []string) error {
	fs := flag.NewFlagSet("teardown", flag.ExitOnError)
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
		return fmt.Errorf("container %q not found", *instanceName)
	}

	slog.Info("removing container", "container_id", container.ID[:12], "name", *instanceName)
	if err := mgr.Remove(ctx, *instanceName); err != nil {
		return fmt.Errorf("failed to remove container: %w", err)
	}

	slog.Info("container removed")
	return nil
}
