package main

import (
	"os"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
)

func main() {
	os.Exit(app.Execute(os.Args[1:], os.Stdout, os.Stderr))
}
