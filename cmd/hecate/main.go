package main

import (
	"os"

	"github.com/shiinobu/hecatedns/backend/controller"
)

func main() {
	os.Exit(controller.Execute(os.Args[1:], os.Stdout, os.Stderr))
}
