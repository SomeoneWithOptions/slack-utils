// Package applog provides quiet-aware progress logging and diagnostic helpers.
package applog

import (
	"fmt"
	"log"
	"os"
	"strings"
)

// Logger writes progress logs that can be silenced with Quiet.
type Logger struct {
	Quiet  bool
	stdout *log.Logger
}

// New returns a Logger that writes progress lines to stdout.
func New() *Logger {
	return &Logger{
		stdout: log.New(os.Stdout, "", log.LstdFlags),
	}
}

// Logf writes a progress log line unless Quiet is set.
func (l *Logger) Logf(format string, args ...any) {
	if l == nil || l.Quiet {
		return
	}
	l.stdout.Printf(format, args...)
}

// Warn prints a non-fatal diagnostic to stderr.
func Warn(err error) {
	if err == nil {
		return
	}
	printDiagnostic(os.Stderr, "warning: ", err.Error())
}

// Fail prints a fatal diagnostic to stderr and exits with status 1.
func Fail(err error) {
	if err == nil {
		return
	}
	printDiagnostic(os.Stderr, "error: ", err.Error())
	os.Exit(1)
}

func printDiagnostic(out *os.File, prefix, message string) {
	message = strings.TrimRight(message, "\n")
	if message == "" {
		fmt.Fprintln(out, prefix)
		return
	}
	for _, line := range strings.Split(message, "\n") {
		fmt.Fprintln(out, prefix+line)
	}
}
