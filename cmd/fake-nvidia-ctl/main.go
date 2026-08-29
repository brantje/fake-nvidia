package main

import (
	"context"
	"fmt"
	"os"

	"github.com/brantje/fake-nvidia/internal/controlcli"
)

func main() {
	if err := controlcli.Run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "fake-nvidia-ctl:", err)
		os.Exit(1)
	}
}
