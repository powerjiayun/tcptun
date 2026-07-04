package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	go handleShutdownSignals(signals, cancel, os.Exit)

	app := buildApp()
	os.Exit(app.MainDefault(ctx, os.Args[1:]))
}

func handleShutdownSignals(signals <-chan os.Signal, cancel context.CancelFunc, exit func(int)) {
	if _, ok := <-signals; !ok {
		return
	}
	cancel()
	if sig, ok := <-signals; ok {
		exit(exitCodeForSignal(sig))
	}
}

func exitCodeForSignal(sig os.Signal) int {
	switch sig {
	case os.Interrupt:
		return 130
	case syscall.SIGTERM:
		return 143
	default:
		return 1
	}
}
