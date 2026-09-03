// dev_port_forward: minimal TCP port forwarder bridging Windows -> WSL-docker
// under WSL mirrored networking (mirrored does not relay docker's published
// ports, but plain socket binds in WSL are reachable from the host).
//
// Usage: go run scripts/dev_port_forward.go LISTEN_PORT:TARGET_PORT [MORE...]
// Example (repo):  go run scripts/dev_port_forward.go 3000:80 18081:18080 &
//
// Dev-only helper; NOT part of the WeKnora runtime.
package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
)

func forward(pair string) {
	parts := strings.SplitN(pair, ":", 2)
	if len(parts) != 2 {
		log.Fatalf("bad pair %q, want LISTEN:TARGET", pair)
	}
	listenPort := parts[0]
	target := "127.0.0.1:" + parts[1]
	if _, err := strconv.Atoi(listenPort); err != nil {
		log.Fatalf("bad listen port %q", listenPort)
	}
	ln, err := net.Listen("tcp", "0.0.0.0:"+listenPort)
	if err != nil {
		log.Fatalf("listen :%s: %v", listenPort, err)
	}
	log.Printf("forwarding 0.0.0.0:%s -> %s", listenPort, target)
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept :%s: %v", listenPort, err)
			continue
		}
		go func(c net.Conn) {
			defer c.Close()
			up, err := net.Dial("tcp", target)
			if err != nil {
				log.Printf("dial %s: %v", target, err)
				return
			}
			defer up.Close()
			done := make(chan struct{}, 2)
			go func() { io.Copy(up, c); done <- struct{}{} }()
			go func() { io.Copy(c, up); done <- struct{}{} }()
			<-done
		}(conn)
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: dev_port_forward LISTEN:TARGET [MORE...]")
		os.Exit(2)
	}
	for _, pair := range os.Args[1:] {
		go forward(pair)
	}
	select {} // run forever
}
