//go:build !windows

package main

// runAsWindowsService is a no-op outside Windows.
func runAsWindowsService(args []string) (handled bool, code int) { return false, 0 }
