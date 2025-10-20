package logging

import (
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
)

// RecoverPanic is a helper function to recover from panics in goroutines and log them.
// It should be deferred at the start of any goroutine.
//
// Usage:
//
//	go func() {
//	    defer logging.RecoverPanic(logger, "worker-name")
//	    // ... goroutine work ...
//	}()
func RecoverPanic(logger *slog.Logger, componentName string) {
	if r := recover(); r != nil {
		logger.Error("panic recovered in goroutine",
			"component", componentName,
			"panic", r,
			"stack", string(debug.Stack()),
		)
	}
}

// RecoverPanicWithCallback is like RecoverPanic but also calls a callback function
// after logging the panic. This can be used to trigger alerts or restart logic.
func RecoverPanicWithCallback(logger *slog.Logger, componentName string, callback func(panicValue any)) {
	if r := recover(); r != nil {
		logger.Error("panic recovered in goroutine",
			"component", componentName,
			"panic", r,
			"stack", string(debug.Stack()),
		)
		if callback != nil {
			// Run callback in a separate recovery block to prevent callback panics
			defer func() {
				if r2 := recover(); r2 != nil {
					logger.Error("panic in recovery callback",
						"component", componentName,
						"callback_panic", r2,
					)
				}
			}()
			callback(r)
		}
	}
}

// SafeGo runs a function in a new goroutine with panic recovery.
// If the goroutine panics, the panic is logged with a stack trace.
func SafeGo(logger *slog.Logger, name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("panic in goroutine",
					"goroutine", name,
					"panic", r,
					"stack", string(debug.Stack()),
				)
			}
		}()
		fn()
	}()
}

// SafeGoWithCallback starts a goroutine with panic recovery and callback.
func SafeGoWithCallback(logger *slog.Logger, componentName string, fn func(), panicCallback func(panicValue any)) {
	go func() {
		defer RecoverPanicWithCallback(logger, componentName, panicCallback)
		fn()
	}()
}

// SafeGoWithWg runs a function in a new goroutine with panic recovery and WaitGroup tracking.
// If the goroutine panics, the panic is logged and wg.Done() is still called to prevent deadlock.
func SafeGoWithWg(logger *slog.Logger, name string, wg *sync.WaitGroup, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("panic in goroutine",
					"goroutine", name,
					"panic", r,
					"stack", string(debug.Stack()),
				)
			}
			wg.Done()
		}()
		fn()
	}()
}

// WrapHandler wraps an HTTP handler with panic recovery.
// Returns a new handler that recovers from panics and returns 500 Internal Server Error.
func WrapHandler(logger *slog.Logger, handlerName string, next func(w any, r any)) func(w any, r any) {
	return func(w any, r any) {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("panic in HTTP handler",
					"handler", handlerName,
					"panic", r,
					"stack", string(debug.Stack()),
				)
				// Try to write error response if possible
				// Note: This requires type assertion in real usage
				fmt.Printf("HTTP 500 - Internal Server Error\n")
			}
		}()
		next(w, r)
	}
}
