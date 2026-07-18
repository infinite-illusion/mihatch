package main

import (
	"os"

	"mihatch/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
