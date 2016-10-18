package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/ERnsTL/flowd/libflowd"
)

func main() {
	// open connection to network
	bufr := bufio.NewReader(os.Stdin)
	// read list of output ports from program arguments
	outPorts := os.Args[1:]
	if len(outPorts) == 0 {
		/*
			fmt.Println("ERROR: no output port names given")
			os.Exit(1)
		*/
		// get configuration from IIP = initial information packet/frame
		fmt.Fprintln(os.Stderr, "wait for IIP")
		if iip, err := flowd.GetIIP("CONF", bufr); err != nil {
			fmt.Fprintln(os.Stderr, "ERROR getting IIP:", err, "- Exiting.")
			os.Exit(1)
		} else {
			outPorts = strings.Split(iip, ",")
			if len(outPorts) == 0 {
				fmt.Fprintln(os.Stderr, "ERROR: no output ports names given in IIP, format is [port],[port],[port]...")
				os.Exit(1)
			}
		}
	}
	fmt.Fprintln(os.Stderr, "got output ports", outPorts)

	var frame *flowd.Frame //TODO why is this pointer to Frame?
	var err error

	for {
		// read frame
		frame, err = flowd.ParseFrame(bufr)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}

		// send it to given output ports
		for _, outPort := range outPorts {
			frame.Port = outPort
			if err := frame.Marshal(os.Stdout); err != nil {
				fmt.Fprintln(os.Stderr, "ERROR: marshaling frame:", err.Error())
			}
		}
	}
}
