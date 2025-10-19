package main

import (
	"sitia.nu/airgap/src/resend"
)

// BuildNumber is set at build time via -ldflags
var BuildNumber = "dev"

func main() {
	resend.Main(BuildNumber)
}
