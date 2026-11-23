package main

import (
	"os"

	"github.com/cristiangirlea/hrn-search/internal/serve"
)

var version = "dev"

func main() { os.Exit(serve.Run(os.Stdin, os.Stdout, os.Stderr, version)) }
