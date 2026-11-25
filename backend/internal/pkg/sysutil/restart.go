// Package sysutil provides system-level utilities for process management.
package sysutil

import "log"

// RestartService triggers a service restart after the HTTP response is sent.
//
// Platform behavior:
//   - Linux: exits the process and relies on systemd Restart=always
//   - Windows: spawns a detached helper that waits for this process to exit,
//     then restarts via native-control.ps1 (if present next to the binary)
//     or by re-launching the current executable
//
// Prerequisites (Linux):
//   - Service configured with Restart=always in the systemd unit file
func RestartService() error {
	return platformRestart()
}

// RestartServiceAsync is a fire-and-forget version of RestartService.
// It logs errors instead of returning them, suitable for goroutine usage.
func RestartServiceAsync() {
	if err := RestartService(); err != nil {
		log.Printf("Service restart failed: %v", err)
		log.Println("Please restart the service manually")
	}
}
