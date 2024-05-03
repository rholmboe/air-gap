package mtu

import (
	"errors"
	"fmt"
	"net"
	"syscall"
	"unsafe"
)

func GetMTU(nic string, target string) (int32, error) {
    // Create a UDP connection
    conn, err := net.Dial("udp", target) // "www.google.com:80"
    if err != nil {
        return -1, fmt.Errorf("Error dialing: %w", err)
    }
    defer conn.Close()

    // Get the file descriptor of the connection
    fd, err := conn.(*net.UDPConn).File()
    if err != nil {
        return -1, fmt.Errorf("Error getting file descriptor: %w", err)
    }
    defer fd.Close()

    // Get the MTU
    var ifreq struct {
        name [16]byte
        mtu  int32
    }
    copy(ifreq.name[:], nic)

    _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd.Fd(), uintptr(syscall.SIOCGIFMTU), uintptr(unsafe.Pointer(&ifreq)))
    if errno != 0 {
        return -1, errors.New("Error getting MTU")
    }

    return ifreq.mtu, nil
}
