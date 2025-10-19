package main

import (
	"runtime"

	"sitia.nu/airgap/src/downstream"
)

// BuildNumber is set at build time via -ldflags
var BuildNumber = "dev"

func main() {
	// Lock the current goroutine to its current OS thread to reduce cpu switching.
	runtime.LockOSThread()

	// Set the maximum number of CPUs that can execute Go code simultaneously to the number of available CPUs.
	// This enables the Go scheduler to use all CPU cores for maximum parallelism.
	runtime.GOMAXPROCS(runtime.NumCPU())

	// Start the main downstream process
	downstream.Main(BuildNumber)
}
