package app

import (
	"errors"
	"syscall"
)

func isPlatformConnectionInterruption(err error) bool {
	return errors.Is(err, syscall.WSAECONNRESET) || errors.Is(err, syscall.WSAECONNABORTED)
}
