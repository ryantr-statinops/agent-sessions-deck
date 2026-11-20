//go:build linux

package owner

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// HandleSignals installs OS signal notify traps for SIGINT, SIGTERM, and SIGHUP.
// Upon receiving a signal, it initiates a bounded graceful shutdown of the runtime.
// The returned cancel function cleans up the signal listener.
func HandleSignals(ctx context.Context, rt *Runtime, grace time.Duration, onComplete func(ShutdownReport, error)) func() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	done := make(chan struct{})

	go func() {
		select {
		case <-sigCh:
			triggerSignalShutdown(rt, grace, onComplete)
		case <-done:
			return
		case <-ctx.Done():
			return
		}
	}()

	return func() {
		close(done)
		signal.Stop(sigCh)
	}
}

func triggerSignalShutdown(rt *Runtime, grace time.Duration, onComplete func(ShutdownReport, error)) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), grace+3*time.Second)
	defer cancel()
	report, err := rt.Shutdown(shutdownCtx, grace)
	if onComplete != nil {
		onComplete(report, err)
	}
}
