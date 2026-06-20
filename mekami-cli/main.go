package main

import (
	"github.com/Wolf258/mekami-cli/cmd/mekami"
	_ "github.com/Wolf258/mekami-core/frontend/all_gen"
)

func main() {
	mekami.Execute()
}
