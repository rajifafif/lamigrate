package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// interruptSignals lists the OS signals that should gracefully cancel
// the running context.  On Unix these are SIGINT and SIGTERM; on
// Windows SIGTERM is accepted by Go even though Windows has no native
// SIGTERM — it receives os.Interrupt only.
var interruptSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM}

// signalContext returns a context that is cancelled when the process
// receives one of the interruptSignals.  The caller should pass this
// context into every long-running operation (up, down, reset, etc.)
// so that cleanup can run.
func signalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, interruptSignals...)
}
