//go:build windows

package main

import (
	"os"
	"syscall"
)

// На Windows нет SIGUSR1/SIGHUP; заглушки нужны только чтобы switch компилировался.
var (
	sigReload os.Signal = dummySignal("SIGHUP")
	sigStats  os.Signal = dummySignal("SIGUSR1")
	sigTerm   os.Signal = syscall.SIGTERM
)

type dummySignal string

func (s dummySignal) String() string { return string(s) }
func (s dummySignal) Signal()        {}

func notifySignals() []os.Signal {
	return []os.Signal{sigTerm, os.Interrupt}
}
