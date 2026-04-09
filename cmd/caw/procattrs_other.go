//go:build !windows

package main

import "syscall"

func brokerSysProcAttr() *syscall.SysProcAttr {
	return nil
}
