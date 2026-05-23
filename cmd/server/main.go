package main

import (
	"fmt"
	"log"

	"github.com/skael-dev/skael/internal/platform"
)

func main() {
	cfg, err := platform.LoadConfig()
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}

	fmt.Printf("skael-server starting on %s\n", cfg.ListenAddr)
}
