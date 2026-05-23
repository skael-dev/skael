package main

import (
	"fmt"
	"os"

	"github.com/skael-dev/skael/internal/platform"
)

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(1)
	}

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: migrate <up|down|status>")
		os.Exit(1)
	}

	db, err := platform.OpenDB(dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "database error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	switch os.Args[1] {
	case "up":
		if err := platform.Migrate(db); err != nil {
			fmt.Fprintf(os.Stderr, "migrate up: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("migrations applied successfully")
	case "down":
		if err := platform.MigrateDown(db); err != nil {
			fmt.Fprintf(os.Stderr, "migrate down: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("last migration rolled back")
	case "status":
		if err := platform.MigrateStatus(db); err != nil {
			fmt.Fprintf(os.Stderr, "migrate status: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s (use up, down, or status)\n", os.Args[1])
		os.Exit(1)
	}
}
