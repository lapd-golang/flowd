package unixfbp

import (
	"bufio"
	"flag"
	"fmt"
	"os"
)

func OpenOutPort(portName string) (netout *bufio.Writer, outPipe *os.File, err error) {
	// check for existence of port
	port, exists := OutPorts[portName]
	if !exists {
		return nil, nil, fmt.Errorf("outport unknown: %s", portName)
	}
	// open named pipe = FIFO
	outPipe, err = os.OpenFile(port.Path, os.O_WRONLY, os.ModeNamedPipe)
	if err != nil {
		return nil, nil, fmt.Errorf("opening outport %s at path %s: %s", portName, port.Path, err)
	}
	// create buffered writer
	netout = bufio.NewWriter(outPipe)
	// return everything, but also keep it here
	port.Pipe = outPipe
	port.Writer = netout
	OutPorts[portName] = port
	return
}

func OpenInPort(portName string) (netin *bufio.Reader, inPipe *os.File, err error) {
	// check for existence of port
	port, exists := InPorts[portName]
	if !exists {
		return nil, nil, fmt.Errorf("inport unknown: %s", portName)
	}
	// open named pipe = FIFO
	inPipe, err = os.OpenFile(port.Path, os.O_RDONLY, os.ModeNamedPipe)
	if err != nil {
		return nil, nil, fmt.Errorf("opening inport %s at path %s: %s", portName, port.Path, err)
	}
	// create buffered writer
	netin = bufio.NewReader(inPipe)
	// return everything, but also keep it here
	port.Pipe = inPipe
	port.Reader = netin
	InPorts[portName] = port
	return
}

// internal state for
var inPortName, outPortName string

type inPortFlag struct{}
type outPortFlag struct{}

// implement flag.Value
func (p inPortFlag) String() string {
	//TODO
	return "inPortFlag.String(): TODO"
}
func (p inPortFlag) Set(value string) error {
	if inPortName == "" {
		// first state; have port name
		inPortName = value
	} else {
		// save entry
		InPorts[inPortName] = InPort{Path: value}
		inPortName = ""
	}
	return nil
}
func (p outPortFlag) String() string {
	//TODO
	return "outPortFlag.String(): TODO"
}
func (p outPortFlag) Set(value string) error {
	if outPortName == "" {
		// first state; have port name
		outPortName = value
	} else {
		// save entry
		OutPorts[outPortName] = OutPort{Path: value}
		outPortName = ""
	}
	return nil
}

type InPort struct {
	Path   string
	Pipe   *os.File
	Reader *bufio.Reader
}
type OutPort struct {
	Path   string
	Pipe   *os.File
	Writer *bufio.Writer
}

var (
	InPorts  = map[string]InPort{}
	OutPorts = map[string]OutPort{}
	Debug    bool
	Quiet    bool
)

// DefFlags sets the most common flags for input and output ports as wll as debug and quiet flags
func DefFlags() {
	//InPorts = map[]string{}
	//OutPorts = map[]string{}
	var inportsFlag inPortFlag
	var outportsFlag outPortFlag
	flag.Var(inportsFlag, "inport", "name of an input port (multiple possile); follow up with -infrom")
	flag.Var(inportsFlag, "inpath", "path of named pipe for previously declared input port (multiple possile); precede with -inport")
	flag.Var(outportsFlag, "outport", "name of an output port (multiple possible); follow up with -outto")
	flag.Var(outportsFlag, "outpath", "path of named pipe for previously declared output port (multiple possle); precede with -outport")
	flag.BoolVar(&Debug, "debug", false, "give detailed event output")
	flag.BoolVar(&Quiet, "quiet", false, "no informational output except errors")
}