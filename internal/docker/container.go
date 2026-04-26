package docker

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

// Container holds information about a managed Docker container.
type Container struct {
	ID       string
	Port     int
	SourceDB string
	PgHost   string
	Password string
	State    string
}

// CreateOpts configures container creation.
type CreateOpts struct {
	Name     string
	Image    string
	PgPort   int
	SourceDB string
	Password string
	PgHost   string
}

// Manager manages pgmint Docker containers.
type Manager struct {
	cli *client.Client
}

// NewManager creates a new Docker Manager using environment configuration.
func NewManager() (*Manager, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}
	return &Manager{cli: cli}, nil
}

// FindByName finds a pgmint container by its name label.
func (m *Manager) FindByName(ctx context.Context, name string) (*Container, error) {
	f := filters.NewArgs()
	f.Add("label", fmt.Sprintf("pgmint.name=%s", name))

	containers, err := m.cli.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: f,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	if len(containers) == 0 {
		return nil, nil
	}

	c := containers[0]
	port, _ := strconv.Atoi(c.Labels["pgmint.pg-port"])

	return &Container{
		ID:       c.ID,
		Port:     port,
		State:    c.State,
		SourceDB: c.Labels["pgmint.source-db"],
		PgHost:   c.Labels["pgmint.pg-host"],
		Password: c.Labels["pgmint.password"],
	}, nil
}

// Create creates and starts a new PostgreSQL Docker container.
func (m *Manager) Create(ctx context.Context, opts CreateOpts) (*Container, error) {
	existing, err := m.FindByName(ctx, opts.Name)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.State != "running" {
			return nil, fmt.Errorf("container %q exists but is not running (state: %s) — run 'teardown' first", opts.Name, existing.State)
		}
		return nil, fmt.Errorf("container %q already exists and is running — run 'teardown' first", opts.Name)
	}

	slog.Info("pulling image", "image", opts.Image)
	reader, err := m.cli.ImagePull(ctx, opts.Image, image.PullOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to pull image: %w", err)
	}
	_, _ = io.Copy(io.Discard, reader)
	reader.Close()

	containerConfig := &container.Config{
		Image: opts.Image,
		Env: []string{
			fmt.Sprintf("POSTGRES_PASSWORD=%s", opts.Password),
		},
		ExposedPorts: nat.PortSet{
			"5432/tcp": struct{}{},
		},
		Labels: map[string]string{
			"pgmint":           "true",
			"pgmint.name":      opts.Name,
			"pgmint.source-db": opts.SourceDB,
			"pgmint.pg-host":   opts.PgHost,
			"pgmint.password":  opts.Password,
			"pgmint.pg-port":   strconv.Itoa(opts.PgPort),
		},
	}

	hostConfig := &container.HostConfig{
		PortBindings: nat.PortMap{
			"5432/tcp": []nat.PortBinding{
				{
					HostIP:   "0.0.0.0",
					HostPort: strconv.Itoa(opts.PgPort),
				},
			},
		},
	}

	resp, err := m.cli.ContainerCreate(ctx, containerConfig, hostConfig, &network.NetworkingConfig{}, nil, opts.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	if err := m.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		_ = m.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	slog.Info("container started", "container_id", resp.ID[:12])

	return &Container{
		ID:       resp.ID,
		Port:     opts.PgPort,
		SourceDB: opts.SourceDB,
		PgHost:   opts.PgHost,
		Password: opts.Password,
		State:    "running",
	}, nil
}

// Remove forcefully removes a container by name.
func (m *Manager) Remove(ctx context.Context, name string) error {
	c, err := m.FindByName(ctx, name)
	if err != nil {
		return err
	}
	if c == nil {
		return fmt.Errorf("container %q not found", name)
	}

	if err := m.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{
		Force: true,
	}); err != nil {
		return fmt.Errorf("failed to remove container: %w", err)
	}

	return nil
}
