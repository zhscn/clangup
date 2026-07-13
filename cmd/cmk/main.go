package main

import (
	"os"

	"github.com/zhscn/clangup/internal/cmk"
)

var version = "dev"

func main() {
	os.Exit(cmk.Run(os.Args[1:], os.Stdout, os.Stderr, version))
}
