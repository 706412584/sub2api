//go:build !windows

package sysutil

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"time"
)

func platformRestart() error {
	if runtime.GOOS != "linux" {
		log.Printf("Service restart via exit only works on Linux with systemd (current OS: %s)", runtime.GOOS)
		return fmt.Errorf("service restart not supported on %s", runtime.GOOS)
	}

	log.Println("Initiating service restart by graceful exit...")
	log.Println("systemd will automatically restart the service (Restart=always)")

	// Give a moment for logs to flush and response to be sent
	go func() {
		time.Sleep(100 * time.Millisecond)
		os.Exit(0)
	}()

	return nil
}
