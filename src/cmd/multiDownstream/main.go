package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"syscall"
)

func main() {
	port := 1234
	instances := runtime.NumCPU() // one per core
	exe, _ := os.Executable()

	fmt.Printf("Launching %d listener processes on port %d...\n", instances, port)

	for i := 0; i < instances; i++ {
		cmd := exec.Command(exe, "--listen", strconv.Itoa(port))
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = append(os.Environ(),
			"GOMAXPROCS=1",
		)
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Setpgid: true, // keep child separate
		}
		// Pin to a specific CPU using taskset (Linux only)
		go func(i int) {
			taskset := exec.Command("taskset", "-c", strconv.Itoa(i), exe, "--listen", strconv.Itoa(port))
			taskset.Stdout = os.Stdout
			taskset.Stderr = os.Stderr
			_ = taskset.Run()
		}(i)
	}

	select {} // block forever
}
