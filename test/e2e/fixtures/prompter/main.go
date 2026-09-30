// Command prompter is an e2e fixture for interactive sessions. On a TTY it
// prints "Name? ", reads a line and prints "hello <name>", then exits 0.
// With -size it first prints "size=<cols>x<rows>". When stdin is not a TTY
// it prints "not a tty" and exits 3.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

func isTerminal(fd uintptr) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlGetTermios, uintptr(unsafe.Pointer(&t)))
	return errno == 0
}

type winsize struct{ Row, Col, X, Y uint16 }

func main() {
	size := flag.Bool("size", false, "print the terminal size first")
	flag.Parse()
	if !isTerminal(os.Stdin.Fd()) {
		fmt.Println("not a tty")
		os.Exit(3)
	}
	if *size {
		var ws winsize
		_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, os.Stdin.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws)))
		fmt.Printf("size=%dx%d\n", ws.Col, ws.Row)
	}
	fmt.Print("Name? ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		fmt.Printf("\nread error: %v\n", err)
		os.Exit(4)
	}
	fmt.Printf("hello %s\n", strings.TrimSpace(line))
}
