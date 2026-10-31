//go:build windows

package store

import "os"

// tryFlock is a no-op on Windows: `syscall.Flock` does not exist there, and SQLite is not the engine any
// deployment runs on. Returning "locked" keeps the development path working rather than failing to compile.
func tryFlock(_ *os.File) (bool, error) { return true, nil }
