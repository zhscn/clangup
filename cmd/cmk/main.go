package main

import "github.com/zhscn/clangup/internal/cmk"

var version = "dev"

func main() {
	cmk.Version = version
	cmk.Main()
}
