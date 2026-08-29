package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/brantje/fake-nvidia/internal/fakellama"
)

func main() {
	cfg, err := fakellama.ParseConfig(os.Args[1:], os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake-llama-server:", err)
		os.Exit(2)
	}
	registry, err := fakellama.NewNVMLRegistryFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake-llama-server:", err)
		os.Exit(2)
	}
	server := fakellama.NewServer(cfg, registry, uint32(os.Getpid()), os.Stdout, os.Stderr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	go func() {
		for sig := range signals {
			if cfg.HangShutdown {
				fmt.Fprintf(os.Stderr, "fake-llama-server: injected shutdown hang after %s; GPU resources released while process remains alive\n", sig)
				_ = server.ReleaseResources(context.Background())
				continue
			}
			fmt.Fprintf(os.Stdout, "fake-llama-server: received %s, shutting down\n", sig)
			cancel()
			return
		}
	}()

	if err := server.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "fake-llama-server:", err)
		if errors.Is(err, fakellama.ErrInjectedCrash) {
			os.Exit(42)
		}
		os.Exit(1)
	}
}
