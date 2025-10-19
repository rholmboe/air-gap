package main

import (
	"runtime"

	"sitia.nu/airgap/src/upstream"
)

// BuildNumber is set at build time via -ldflags
var BuildNumber = "dev"

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	upstream.Main(BuildNumber)
}
