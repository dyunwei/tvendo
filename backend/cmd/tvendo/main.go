package main

import (
	"os"
	"tvendo/internal/cli"
)

var version = "0.1.0"

func main() { os.Exit(cli.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, version)) }
