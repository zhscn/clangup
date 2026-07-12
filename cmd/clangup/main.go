package main

import (
	"os"

	"github.com/zhscn/clangup/internal/clangup"
)

var version = "dev"

func main() {
	os.Exit(clangup.Run(os.Args[1:], os.Stdout, os.Stderr, version))
}
