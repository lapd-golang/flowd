package main

import (
	"fmt"
	"io"
	"net"
	"os"
)

func main() {
	if len(os.Args) < 1+1 {
		fmt.Println("Usage:", os.Args[0], "[host]:[port]")
		os.Exit(1)
	}

	fmt.Println("resolving address")
	serverAddr, err := net.ResolveTCPAddr("tcp", os.Args[1])
	CheckError(err)

	fmt.Println("open socket")
	listener, err := net.ListenTCP("tcp", serverAddr)
	CheckError(err)

	fmt.Println("listening...")
	conn, err := listener.AcceptTCP()
	fmt.Println("accepted connection")
	defer conn.Close()

	fmt.Println("closing listener")
	listener.Close()

	fmt.Println("waiting for incoming data")
	fmt.Println("ctrl-c to close connection")

	// copy UDP connection to STDOUT
	io.Copy(os.Stdout, conn)
}

func CheckError(err error) {
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(2)
	}
}
