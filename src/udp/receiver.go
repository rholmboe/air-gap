package udp

import (
	"fmt"
	"log"
	"net"
	"sync"
)

// Handle a connection
// Just call the callback function with the data for every received packet
func handleConnection(conn *net.UDPConn, addr *net.UDPAddr, wg *sync.WaitGroup, callback func(arg []byte), mtu uint16) {
    defer wg.Done()

    buf := make([]byte, mtu)
    n, _, err := conn.ReadFromUDP(buf)
    if err != nil {
        fmt.Println(err)
        return
    }
	callback(buf[:n])
}

// Start a UDP listener on the assigned port and address
func ListenUDP(address string, port int, callback func(arg []byte), mtu uint16) {
    addr := net.UDPAddr{
        Port: port,
        IP:   net.ParseIP(address),
    }

    conn, err := net.ListenUDP("udp", &addr)
    if err != nil {
        fmt.Println(err)
        return
    }
    defer conn.Close()

    log.Printf("Listening on %s\n", addr.String())

    var wg sync.WaitGroup

    for {
        wg.Add(1)
        go func() {
            handleConnection(conn, &addr, &wg, callback, mtu)
        }()
        wg.Wait()
    }
}
