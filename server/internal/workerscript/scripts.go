// Package workerscript owns the embedded Linux Worker scripts without depending
// on the control plane's HTTP handlers or database implementation.
package workerscript

import _ "embed"

//go:embed install.sh
var workerInstall string

//go:embed daemon.sh
var workerDaemon string

func Install() string { return workerInstall }

func Daemon() string { return workerDaemon }
