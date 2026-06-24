package main

import "golang.org/x/sys/unix"

// unixRenameat envuelve renameat(2) para el rename TOCTOU-safe basado en fds.
func unixRenameat(oldfd int, oldpath string, newfd int, newpath string) error {
	return unix.Renameat(oldfd, oldpath, newfd, newpath)
}
