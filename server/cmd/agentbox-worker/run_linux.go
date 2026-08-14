//go:build linux

package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"agentbox/internal/httpapi"
	"golang.org/x/sys/unix"
)

func runWorkerShell(arguments []string) error {
	descriptor, err := unix.MemfdCreate("agentbox-worker", 0)
	if err != nil {
		return fmt.Errorf("create Worker script memory file: %w", err)
	}
	file := os.NewFile(uintptr(descriptor), "agentbox-worker")
	defer file.Close()
	if _, err := io.WriteString(file, httpapi.WorkerDaemonScript()); err != nil {
		return fmt.Errorf("write Worker script memory file: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind Worker script memory file: %w", err)
	}
	scriptPath := fmt.Sprintf("/proc/self/fd/%d", descriptor)
	argv := []string{"sh", scriptPath}
	argv = append(argv, arguments...)
	return syscall.Exec("/bin/sh", argv, workerEnvironment(os.Environ()))
}

func workerEnvironment(environment []string) []string {
	const key = "AGENTBOX_WORKER_VERSION="
	result := make([]string, 0, len(environment)+1)
	for _, value := range environment {
		if !strings.HasPrefix(value, key) {
			result = append(result, value)
		}
	}
	return append(result, key+version)
}
