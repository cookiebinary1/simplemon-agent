// Copyright (c) 2024 simplemon
// SPDX-License-Identifier: MIT
//
// Package logger provides a simple logging wrapper around slog.

package logger

import (
	"log/slog"
	"os"
	"sync"
)

var (
	// Log is the global logger instance
	Log      *slog.Logger
	logMu    sync.RWMutex
	initOnce sync.Once
)

func init() {
	// Initialize with default logger to prevent nil pointer issues
	Log = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// Init initializes the global logger with the specified verbosity level.
// It is safe to call Init multiple times, but only the first call will
// have an effect. Use InitForce to reinitialize the logger.
func Init(verbose bool) {
	initOnce.Do(func() {
		initLogger(verbose)
	})
}

// InitForce reinitializes the logger regardless of previous initialization.
// This should only be used in tests.
func InitForce(verbose bool) {
	logMu.Lock()
	defer logMu.Unlock()
	initLogger(verbose)
}

func initLogger(verbose bool) {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}

	opts := &slog.HandlerOptions{
		Level: level,
	}

	handler := slog.NewTextHandler(os.Stdout, opts)
	Log = slog.New(handler)
	slog.SetDefault(Log)
}

// getLog returns the current logger safely.
func getLog() *slog.Logger {
	logMu.RLock()
	l := Log
	logMu.RUnlock()
	return l
}

// Debug logs a debug message
func Debug(msg string, args ...any) {
	getLog().Debug(msg, args...)
}

// Info logs an info message
func Info(msg string, args ...any) {
	getLog().Info(msg, args...)
}

// Warn logs a warning message
func Warn(msg string, args ...any) {
	getLog().Warn(msg, args...)
}

// Error logs an error message
func Error(msg string, args ...any) {
	getLog().Error(msg, args...)
}

// With returns a logger with additional attributes
func With(args ...any) *slog.Logger {
	return getLog().With(args...)
}
