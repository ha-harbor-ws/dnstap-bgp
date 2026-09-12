//go:build !windows

package main

import (
	"os"
	"syscall"
)

var (
	sigReload os.Signal = syscall.SIGHUP
	sigStats  os.Signal = syscall.SIGUSR1
	sigTerm   os.Signal = syscall.SIGTERM
)

func notifySignals() []os.Signal {
	return []os.Signal{sigTerm, sigReload, sigStats, os.Interrupt}
}
