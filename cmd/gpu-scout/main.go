package main

import (
	"os"

	"github.com/andrejvysny/gpu-scout/internal/scout"
)

func main() {
	os.Exit(scout.CLI(os.Args[1:], os.Stdout, os.Stderr))
}
