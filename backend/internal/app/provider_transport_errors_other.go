//go:build !windows

package app

func isPlatformConnectionInterruption(error) bool { return false }
