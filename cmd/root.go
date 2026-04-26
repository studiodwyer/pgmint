package cmd

import (
	"fmt"
	"log/slog"
	"os"
)

var Version = "dev"

var Defaults = struct {
	Name          string
	PgPort        int
	PostgresImage string
	SourceDB      string
	PgHost        string
	Password      string
	ListenAddr    string
	DaemonAddr    string
}{
	Name:          "pgmint",
	PgPort:        5432,
	PostgresImage: "postgres:13",
	SourceDB:      "sourcedb",
	PgHost:        "localhost",
	Password:      "postgres",
	ListenAddr:    "0.0.0.0:9876",
	DaemonAddr:    "127.0.0.1:9876",
}

// Execute runs the pgmint CLI.
func Execute() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	setupLogger(args)
	args = stripDebug(args)

	var err error
	switch cmd {
	case "init":
		err = runInit(args)
	case "serve":
		err = runServe(args)
	case "connection":
		err = runConnection(args)
	case "clone":
		err = runClone(args)
	case "list":
		err = runList(args)
	case "destroy":
		err = runDestroy(args)
	case "teardown":
		err = runTeardown(args)
	case "version", "-v", "--version":
		fmt.Println("pgmint " + Version)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %q\n", cmd)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func setupLogger(args []string) bool {
	debug := false
	for _, a := range args {
		if a == "--debug" {
			debug = true
		}
	}
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
	return debug
}

func stripDebug(args []string) []string {
	filtered := make([]string, 0, len(args))
	for _, a := range args {
		if a != "--debug" {
			filtered = append(filtered, a)
		}
	}
	return filtered
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `Usage: pgmint <command> [flags]

Commands:
  init        Start postgres container and create source database
  serve       Start the HTTP daemon for clone management
  connection  Print source database connection string
  clone       Request a clone from the daemon
  list        List active clones
  destroy     Destroy a clone
  teardown    Stop and remove the container
  version     Show version

Global flags:
  --debug     Enable debug logging
  --name      Instance name (default "pgmint")

Use "pgmint <command> --help" for command-specific flags.
`)
}
