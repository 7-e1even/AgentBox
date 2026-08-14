//go:build !linux

package main

import "errors"

func runWorkerShell([]string) error {
	return errors.New("AgentBox Worker requires Linux")
}
