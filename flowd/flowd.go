package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"

	termutil "github.com/andrew-d/go-termutil"
	"github.com/oleksandr/fbp"
)

func main() {
	if termutil.Isatty(os.Stdin.Fd()) {
		fmt.Println("ERROR: nothing piped on STDIN, expected network definition")
	} else {
		fmt.Println("ok, found something piped on STDIN")

		// read network definition
		nwBytes, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}

		// parse and validate network
		nw := &fbp.Fbp{Buffer: (string)(nwBytes)}
		fmt.Println("init")
		nw.Init()
		fmt.Println("parse")
		if err := nw.Parse(); err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
		fmt.Println("execute")
		nw.Execute()
		fmt.Println("validate")
		if err := nw.Validate(); err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
		fmt.Println("network definition OK")

		// display all data
		fmt.Println("subgraph name:", nw.Subgraph)
		fmt.Println("processes:")
		for _, p := range nw.Processes {
			fmt.Println(" ", p.String())
		}
		fmt.Println("connections:")
		for _, c := range nw.Connections {
			fmt.Println(" ", c.String())
		}
		fmt.Println("input ports:")
		for name, i := range nw.Inports {
			fmt.Printf(" %s: %s\n", name, i.String())
		}
		fmt.Println("output ports:")
		for name, o := range nw.Outports {
			fmt.Printf(" %s: %s\n", name, o.String())
		}

		// network definition sanity checks
		//TODO

		// decide placement (know available machines, access method to them)
		//TODO

		// generate network data structures
		// prepare list of processes
		procs := make(map[string]*Process)
		for _, fbpProc := range nw.Processes {
			proc := NewProcess(fbpProc)
			if _, exists := procs[proc.Name]; exists {
				// error case
				fmt.Println("ERROR: process already exists by that name:", proc.Name)
				os.Exit(1)
			}
			procs[proc.Name] = proc
		}
		// add connections
		for _, fbpConn := range nw.Connections {
			// prepare connection data
			// check source
			if fbpConn.Source != nil { // regular connection
				//TODO implement
			} else if fbpConn.Data != "" { // source is IIP
				//TODO implement
				fmt.Printf("ERROR: connection with IIP %s to %s: currently unimplemented\n", fbpConn.Data, fbpConn.Target)
				os.Exit(2)
			}
			// check target
			if fbpConn.Target != nil {
				// regular connection
				//TODO implement
			} else {
				// check for outport, otherwise error case (unknown situation)
				//TODO implement
			}

			fmt.Printf("connection: source=%s, target=%s, data=%s\n", fbpConn.Source, fbpConn.Target, fbpConn.Data)
			fromPort := GeneratePortName(fbpConn.Source)
			toPort := GeneratePortName(fbpConn.Target)

			fromProc := fbpConn.Source.Process
			toProc := fbpConn.Target.Process

			// connecting output port
			procs[fromProc].OutPorts = append(procs[fromProc].OutPorts, Port{
				LocalName: fromPort,
				//TODO currently unused
				//LocalAddress: "unix://@flowd/" + fromProc,
				RemoteName:    toPort,
				RemoteAddress: "unix://@flowd/" + toProc,
			})

			// listen input port
			procs[toProc].InPorts = append(procs[toProc].InPorts, Port{
				LocalName:    toPort,
				LocalAddress: "unix://@flowd/" + toProc,
				RemoteName:   fromPort,
				//TODO currently unused
				//RemoteAddress: "unix://@flowd/" + fromProc,
			})
		}
		// add network inports
		for name, iport := range nw.Inports {
			// check if destination exists
			found := false
			for _, proc := range nw.Processes {
				if proc.Name == iport.Process {
					found = true
					break
				}
			}
			if found {
				// destination exists
				fmt.Printf("connection: inport %s -> %s.%s\n", name, iport.Process, iport.Port)
			} else {
				// destination missing
				fmt.Println("ERROR: destination process missing for inport", name)
				os.Exit(2)
			}

			// add connections
			//TODO need flag to know where to listen on (or FBP metadata)
			//TODO implement
		}
		// add network outports
		for name, oport := range nw.Outports {
			// check if source exists
			found := false
			for _, proc := range nw.Processes {
				if proc.Name == oport.Process {
					found = true
					break
				}
			}
			if found {
				// source exists
				fmt.Printf("connection: %s.%s -> outport %s\n", oport.Process, oport.Port, name)
			} else {
				// source missing
				fmt.Println("ERROR: source process missing for outport", name)
				os.Exit(2)
			}

			// add to connections
			//TODO need flag to know where that port goes to
			//TODO implement
		}

		// subscribe to ctrl+c to do graceful shutdown
		//TODO

		// launch network
		// TODO display launch stdout
		for _, proc := range nw.Processes {
			//TODO exit channel to goroutine
			//TODO exit channel from goroutine
			fmt.Printf("launching %s (component: %s)\n", proc.Name, proc.Component)

			//TODO need to have ports generated here -> generate arguments for launch

			go func() {
				// start component as subprocess, with arguments
				//TODO generate arguments, which arguments?
				cmd := exec.Command(flag.Arg(0), flag.Args()[1:]...)
				cout, err := cmd.StdoutPipe()
				if err != nil {
					fmt.Println("ERROR: could not allocate pipe from component stdout:", err)
				}
				cin, err := cmd.StdinPipe()
				if err != nil {
					fmt.Println("ERROR: could not allocate pipe to component stdin:", err)
				}
				cmd.Stderr = os.Stderr
				if err := cmd.Start(); err != nil {
					fmt.Println("ERROR:", err)
					os.Exit(2)
				}
				defer cout.Close()
				defer cin.Close()
			}()
		}

		// detect voluntary network shutdown (how to decide that it should happen?)
		//TODO
	}
}

// NOTE: enums in Go @ https://stackoverflow.com/questions/14426366/what-is-an-idiomatic-way-of-representing-enums-in-go
type Placement int

const (
	Local Placement = iota
	Remote
)

type Process struct {
	Path         string
	Placement    Placement
	Architecture string //x86, x86_64, armv7l, armv8
	Name         string
	InPorts      []Port
	OutPorts     []Port
}

type Port struct {
	LocalName    string
	LocalAddress string

	RemoteName    string
	RemoteAddress string

	IIP string
}

func NewProcess(proc *fbp.Process) *Process {
	//TODO make use of arguments in proc.Metadata
	return &Process{Path: proc.Component, Name: proc.Name, InPorts: []Port{}, OutPorts: []Port{}}
}

func GeneratePortName(endpoint *fbp.Endpoint) string {
	if endpoint.Index == nil {
		return endpoint.Port
	} else {
		return fmt.Sprintf("%s[%d]", endpoint.Port, *endpoint.Index)
	}
}