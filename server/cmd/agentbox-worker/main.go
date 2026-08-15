package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"agentbox/internal/sessionworker"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		_, _ = fmt.Fprintln(os.Stdout, version)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "guest-fs" {
		if err := runGuestFS(os.Args[2:], os.Stdin, os.Stdout); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "image-to-oci" {
		if err := runImageToOCI(os.Args[2:], os.Stdout); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "session" {
		runSessionWorker()
		return
	}
	if err := runWorkerShell(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runSessionWorker() {
	configPath := "/etc/agentbox-worker.conf"
	if len(os.Args) > 2 {
		configPath = os.Args[2]
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := sessionworker.Run(ctx, configPath, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
