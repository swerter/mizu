package logging

import (
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestRecoverPanic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Test with panic
	func() {
		defer RecoverPanic(logger, "test-component")
		panic("test panic")
	}()

	// If we get here, recovery worked
	t.Log("✓ RecoverPanic successfully caught panic")
}

func TestRecoverPanicNoPanic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Test without panic
	func() {
		defer RecoverPanic(logger, "test-component")
		// Normal execution
	}()

	t.Log("✓ RecoverPanic works with no panic")
}

func TestRecoverPanicWithCallback(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var callbackCalled bool
	var panicValue any

	// Test with callback
	func() {
		defer RecoverPanicWithCallback(logger, "test-component", func(p any) {
			callbackCalled = true
			panicValue = p
		})
		panic("test panic with callback")
	}()

	if !callbackCalled {
		t.Error("Callback was not called")
	}
	if panicValue != "test panic with callback" {
		t.Errorf("Expected panic value 'test panic with callback', got %v", panicValue)
	}
}

func TestRecoverPanicWithCallbackPanic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Test with callback that panics (should be caught)
	func() {
		defer RecoverPanicWithCallback(logger, "test-component", func(p any) {
			panic("callback panic")
		})
		panic("original panic")
	}()

	// If we get here, both panics were recovered
	t.Log("✓ RecoverPanicWithCallback handled callback panic")
}

func TestRecoverPanicWithCallbackNil(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Test with nil callback
	func() {
		defer RecoverPanicWithCallback(logger, "test-component", nil)
		panic("test panic")
	}()

	t.Log("✓ RecoverPanicWithCallback works with nil callback")
}

func TestSafeGo(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var wg sync.WaitGroup

	// Test SafeGo with panic
	wg.Add(1)
	SafeGo(logger, "test-goroutine", func() {
		defer wg.Done()
		panic("goroutine panic")
	})

	// Wait for goroutine to complete
	wg.Wait()

	t.Log("✓ SafeGo recovered from panic in goroutine")
}

func TestSafeGoNoPanic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var wg sync.WaitGroup
	executed := false

	// Test SafeGo without panic
	wg.Add(1)
	SafeGo(logger, "test-goroutine", func() {
		defer wg.Done()
		executed = true
	})

	// Wait for goroutine to complete
	wg.Wait()

	if !executed {
		t.Error("Goroutine did not execute")
	}
}

func TestSafeGoWithCallback(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	callbackCalled := make(chan bool, 1)

	// Test SafeGoWithCallback
	SafeGoWithCallback(logger, "test-goroutine", func() {
		panic("goroutine panic")
	}, func(p any) {
		callbackCalled <- true
	})

	// Wait for callback
	select {
	case <-callbackCalled:
		t.Log("✓ SafeGoWithCallback called callback after panic")
	case <-time.After(1 * time.Second):
		t.Error("Callback was not called within timeout")
	}
}

func TestWrapHandler(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	called := false

	// Create wrapped handler
	handler := WrapHandler(logger, "test-handler", func(w any, r any) {
		called = true
		panic("handler panic")
	})

	// Call handler (should recover)
	handler(nil, nil)

	if !called {
		t.Error("Handler was not called")
	}

	t.Log("✓ WrapHandler recovered from panic in handler")
}

func TestWrapHandlerNoPanic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	called := false

	// Create wrapped handler
	handler := WrapHandler(logger, "test-handler", func(w any, r any) {
		called = true
	})

	// Call handler
	handler(nil, nil)

	if !called {
		t.Error("Handler was not called")
	}
}
