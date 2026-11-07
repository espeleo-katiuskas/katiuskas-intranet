package httpapi

import "sync"

// background tracks the work that runs in its own goroutine after a request has been answered or
// that no request waits for — the Telegram notifications, the emails — so that a shutdown can let
// it finish instead of cutting it off.
var background sync.WaitGroup

// Go runs f in its own goroutine and tracks it for WaitBackground. Only for work that ends by
// itself soon: anything that sleeps until some deadline would hold a shutdown up for as long.
func Go(f func()) {
	background.Add(1)
	go func() {
		defer background.Done()
		f()
	}()
}

// WaitBackground blocks until all the work started with Go has finished. Callers that cannot wait
// forever run it in a goroutine of their own and give up after a while.
func WaitBackground() {
	background.Wait()
}
