package main

import (
	"fmt"
	"os"

	"github.com/LinkMaq/kube-accelerator-sim/internal/version"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Print(version.Human("kasim-controller"))
		return
	}

	fmt.Fprintln(os.Stderr, "kasim-controller runtime is not available in this build")
	os.Exit(2)
}
