package main

import "sitia.nu/airgap/src/create"

// BuildNumber is set at build time via -ldflags
var BuildNumber = "dev"

func main() {
	create.Main(BuildNumber, create.NewKafkaReader())
}
