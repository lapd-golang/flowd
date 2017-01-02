package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ERnsTL/flowd/libflowd"
	"github.com/miolini/datacounter"
)

const bufSize = 65536

// type for component connection endpoint definition
type endpoint struct {
	Url  *url.URL
	Addr net.Addr
	// TODO unoptimal to have both Conn and Listener on same struct
	Conn       net.Conn
	Listener   net.Listener
	listenPort string
	Ready      chan bool
	// port names
	LocalPort  string
	RemotePort string
}

type inputEndpoint endpoint
type outputEndpoint endpoint

// types to hold information on a collection of endpoints, ie. all input endpoints
// NOTE: using map of pointers, because map elements are not addressable
type inputEndpoints map[string]*inputEndpoint
type outputEndpoints map[string]*outputEndpoint

// implement flag.Value interface
func (e *inputEndpoints) String() string {
	for _, endpoint := range *e {
		return fmt.Sprintf("%s://%s#%s", endpoint.Url.Scheme, endpoint.Url.Host, endpoint.Url.Fragment)
	}
	return ""
}
func (e *outputEndpoints) String() string {
	for _, endpoint := range *e {
		return fmt.Sprintf("%s://%s#%s", endpoint.Url.Scheme, endpoint.Url.Host, endpoint.Url.Fragment)
	}
	return ""
}

// NOTE: can be called multiple times if there are multiple occurrences of the -in resp. -out flags
// NOTE: if only one occurrence shall be allowed, check if a required property is already set
func (e *inputEndpoints) Set(value string) error {
	if parsedUrl, err := flowd.ParseEndpointURL(value); err != nil {
		return err
	} else {
		(*e)[fragmentLocal(parsedUrl.Fragment)] = &inputEndpoint{
			Url:       parsedUrl,
			LocalPort: fragmentLocal(parsedUrl.Fragment),
		}
	}
	return nil
}
func (e *outputEndpoints) Set(value string) error {
	if parsedUrl, err := flowd.ParseEndpointURL(value); err != nil {
		return err
	} else {
		(*e)[fragmentLocal(parsedUrl.Fragment)] = &outputEndpoint{
			Url:        parsedUrl,
			LocalPort:  fragmentLocal(parsedUrl.Fragment),
			RemotePort: fragmentRemote(parsedUrl.Fragment),
		}
	}
	return nil
}

func (e *outputEndpoint) Dial() {
	e.Ready = make(chan bool)
	go func() {
		try := 1
	tryagain: //TODO could go into infinite loop. later the orchestrator has to be able to detect temporary errors
		oconn, err := net.DialTimeout(e.Url.Scheme, e.Url.Host+e.Url.Path, 10*time.Second)
		if err != nil {
			nerr, ok := err.(net.Error)
			if ok && try <= 10 {
				if try > 5 {
					fmt.Fprintln(os.Stderr, "WARNING: could not dial connection and/or resolve address:", err, "error is permanent?", nerr.Temporary())
				}
				time.Sleep(1 * time.Second)
				try++
				goto tryagain
			} else {
				fmt.Fprintln(os.Stderr, "ERROR: could not dial connection and/or resolve address:", err)
			}
			os.Exit(3)
		}
		e.Conn = oconn
		e.Ready <- true
	}()
}

func (e *inputEndpoint) Listen() {
	ilistener, err := net.Listen(e.Url.Scheme, e.Url.Host+e.Url.Path)
	e.Listener = ilistener
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR: listening on ", e.Url.Host, ":", err)
		os.Exit(4)
	}

	// find out which port the listener has actually bound to
	// NOTE: may be different in case of port 0
	if e.Url.Scheme == "unix" || e.Url.Scheme == "unixpacket" {
		e.listenPort = e.Url.Host + e.Url.Path
	} else {
		// for protocols with [host]:[port] format
		_, actualPort, _ := net.SplitHostPort(ilistener.Addr().String())
		//actualPort := strconv.Itoa(port)
		//TODO decide whether to keep string or int representation
		e.listenPort = actualPort
	}

	// accept one connection
	e.Ready = make(chan bool)
	go func(ep *inputEndpoint) {
		// wait for incoming connection
		if iconn, err := ep.Listener.Accept(); err != nil { //TODO accept with timeout -> so that orchestrator can detect something being wrong
			fmt.Fprintln(os.Stderr, "ERROR: accepting connection on", ep.Listener.Addr(), ":")
			os.Exit(4)
		} else {
			ep.Conn = iconn
			ep.Listener.Close() // close listener for further connections
			ep.Ready <- true
		}
	}(e)
}

type NoArgsNoRetFunc func()

func (e *inputEndpoint) ListenAgain(callback NoArgsNoRetFunc) {
	e.Listen()
	<-e.Ready
	callback()
}

//TODO seems useless, because Conn.Close can be called directly
func (e *inputEndpoint) Close() {
	e.Conn.Close()
	// TODO what if listener is not connected yet? close it as well?
	_ = e.Listener.Close()
}
func (e *outputEndpoint) Close() {
	e.Conn.Close()
}

func (e *inputEndpoints) Listen() {
	for name, ep := range *e {
		if !quiet {
			fmt.Println("connecting input", name)
		}
		ep.Listen()
	}
}
func (e *outputEndpoints) Dial() {
	for name, ep := range *e {
		if !quiet {
			fmt.Println("connecting output", name)
		}
		ep.Dial()
	}
}
func (e *inputEndpoints) Close() {
	for _, ep := range *e {
		ep.Close()
	}
}
func (e *outputEndpoints) Close() {
	for _, ep := range *e {
		ep.Close()
	}
}

// type to hold information about input frames/IPs, which are sent once as first packets onto the given input endpoint/port
type initialIPs map[string]string

// implement flag.Value interface
func (iips *initialIPs) String() (str string) {
	for inPort, data := range *iips {
		if str != "" {
			str += ","
		}
		str += fmt.Sprintf("%s:%s", inPort, data)
	}
	return str
}
func (iips *initialIPs) Set(value string) error {
	//TODO decide if : is really a good separator in practice or if = would be a better separator
	if iip := strings.SplitN(value, ":", 2); len(iip) != 2 {
		return fmt.Errorf("malformed argument for flag -iip: %s", iip[0])
	} else {
		// split fine
		(*iips)[iip[0]] = iip[1]
	}
	return nil
}

// global variables
var (
	debug, quiet bool
)

func main() {
	// read program arguments
	inEndpoints := inputEndpoints{}
	outEndpoints := outputEndpoints{}
	iips := initialIPs{}
	var help bool
	var inFraming, outFraming bool
	flag.Var(&inEndpoints, "in", "input endpoint(s) in URL format, ie. tcp://localhost:0#portname")
	flag.Var(&outEndpoints, "out", "output endpoint(s) in URL format, ie. tcp://localhost:0#portname")
	flag.Var(&iips, "iip", "initial information packet/frame to be sent to an input port, ie. portname:freeformdata")
	flag.BoolVar(&inFraming, "inframing", true, "perform frame decoding and routing on input endpoints")
	flag.BoolVar(&outFraming, "outframing", true, "perform frame decoding and routing on output endpoints")
	flag.BoolVar(&help, "h", false, "print usage information")
	flag.BoolVar(&debug, "debug", false, "give detailed event output")
	flag.BoolVar(&quiet, "quiet", false, "no informational output except errors")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Println("ERROR: missing command to run")
		printUsage()
	}
	if help {
		printUsage()
	}

	// if IIPs are requested, then inFraming is required
	if len(iips) > 0 && !inFraming {
		fmt.Println("ERROR: use of -iip flag requires -inframing=true")
		os.Exit(1)
	}

	// check if the ports named in -iip flags exist
	// NOTE: this check is not required, because it is common that this port has no network endpoint, eg. a CONF or OPTIONS port
	/*
		for _, port := range iips {
			if _, exists := inEndpoints[port]; !exists {
				fmt.Println("ERROR: the input port given in -iip flag does not exist:", port)
			}
		}
	*/

	// connect with other components
	outEndpoints.Dial()
	inEndpoints.Listen()
	defer inEndpoints.Close()
	defer outEndpoints.Close()

	if _, exists := inEndpoints["in"]; exists {
		//TODO what if it does not exist? what to output? what if there is more than one input endpoint?
		//TODO publish any port numbers on Zeroconf? maybe only the processing framework's inports -> move this to flowd
		// return port number
		fmt.Println(inEndpoints["in"].listenPort) //FIXME port "in" may not exist

		// make discoverable so that other components can connect
		//TODO publish all input ports
		//TODO does avahi maybe allow publishing Unix domain addresses too?
		if !(inEndpoints["in"].Url.Scheme == "unix" || inEndpoints["in"].Url.Scheme == "unixpacket") {
			var proto string
			switch inEndpoints["in"].Url.Scheme {
			case "tcp", "tcp4", "tcp6":
				proto = "tcp"
			case "udp", "udp4", "udp6":
				proto = "udp"
			}
			pub := exec.Command("avahi-publish-service", "--service", "--subtype", "_web._sub._flowd._"+proto, "some component", "_flowd._"+proto, inEndpoints["in"].listenPort, "sometag=true")
			if err := pub.Start(); err != nil {
				fmt.Println("ERROR:", err)
				os.Exit(4)
			}
			defer pub.Process.Kill()
		}
	}

	// wait for connections to become ready, otherwise we start the component without all connections set up and it might panic
	//TODO make it possible to see realtime updates when one is connected (1st one may block displaying "Ready" for the others)
	/*
		Select on multiple channels. Make Dial() methods submit to central ready channel (1 for inputs, 1 for outputs) and expect len(inEndpoints+outEndpoints) of ready notifications
		for i := 1; i <= 9; i++ {
		     select {
		     case msg := <-c1:
		          println(msg)
			 case msg := <-c2:
		          println(msg)
		     case msg := <-c3:
		          println(msg)
		     }
		}
	*/
	for name, _ := range inEndpoints {
		ep := inEndpoints[name] //TODO not sure if this is necessary
		if debug {
			fmt.Println("waiting for ready from input", name)
		}
		<-ep.Ready
		if !quiet {
			fmt.Println("input", name, "is now connected")
		}
	}
	for name, _ := range outEndpoints {
		ep := outEndpoints[name] //TODO not sure if this is necessary
		if debug {
			fmt.Println("waiting for ready from output", name)
		}
		<-ep.Ready
		if !quiet {
			fmt.Println("output", name, "is now connected")
		}
	}

	// start component as subprocess, with arguments
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

	// input = transfer input network endpoint to component stdin
	if debug {
		fmt.Println("input framing set to", inFraming)
	}
	if !inFraming {
		// NOTE: using Go stdlib without any processing
		// NOTE: io.Copy copies from right argument to left
		for inEndpoint := range inEndpoints {
			go func(ep *inputEndpoint) {
				// setup direct copy without processing (since unframed)
				if _, err := io.Copy(cin, ep.Conn); err != nil {
					fmt.Println("ERROR: receiving from network input:", err, "Closing.")
					ep.Conn.Close()
					return
				}
			}(inEndpoints[inEndpoint])
		}
	} else {
		// NOTE: using manual buffering (no io.Copy), though more debug output and we can do framing = manipulation of data in transit
		// first deliver initial information packets/frames
		if len(iips) > 0 {
			for port, data := range iips {
				iip := &flowd.Frame{
					Type:        "data",
					BodyType:    "IIP", //TODO maybe this could be user-defined, but would make argument-passing more complicated for little return
					Port:        port,
					ContentType: "text/plain", // is a string from commandline, unlikely to be binary = application/octet-stream, no charset info needed //TODO really?
					Extensions:  nil,
					Body:        []byte(data),
				}
				if !quiet {
					fmt.Println("in xfer 1 IIP to", port)
				}
				if err := iip.Marshal(cin); err != nil {
					fmt.Println("ERROR sending IIP to port", port, ": ", err, "- Exiting.")
					os.Exit(3)
				}
			}
			// GC it
			iips = nil
		}
		// now handle regular packets/frames
		for inEndpoint := range inEndpoints {
			go handleInputEndpoint(inEndpoints[inEndpoint], cin)
		}
	}

	// output = transfer component stdout to output network endpoint
	if debug {
		fmt.Println("output framing set to", outFraming)
	}
	if !outFraming {
		// NOTE: using Go stdlib, without processing
		for outEndpoint := range outEndpoints {
			go func(ep *outputEndpoint) {
				if bytes, err := io.Copy(ep.Conn, cout); err != nil {
					fmt.Println("ERROR: writing to network output:", err, "Closing.")
					ep.Conn.Close()
					return
				} else {
					fmt.Println("net output reached EOF. copied", bytes, "bytes from component stdout -> connection")
				}
			}(outEndpoints[outEndpoint])
		}
	} else {
		// NOTE: this using manual buffering
		go handleOutputEndpoint(outEndpoints, cout)
	}

	// trigger on signal (SIGHUP, SIGUSR1, SIGUSR2, etc.) to reconnect, reconfigure etc.
	//TODO

	// declare network ports
	//TODO

	// post success
	//TODO subprocess logger
	//TODO logger -t flowd -p daemon.info/error/crit/emerg "Starting up"

	cmd.Wait()
	// send out any remaining output from component stdout
	//TODO do that using done channel or similar
	time.Sleep(2 * time.Second)
}

func printUsage() {
	fmt.Println("Usage:", os.Args[0], "-in [input-endpoint(s)]", "-out [output-endpoint(s)]", "[component-cmd]", "[component-args...]")
	flag.PrintDefaults()
	os.Exit(1)
}

func handleInputEndpoint(ep *inputEndpoint, cin io.WriteCloser) {
	var oldCount uint64
	countr := datacounter.NewReaderCounter(ep.Conn)
	bufr := bufio.NewReader(countr)
	for {
		/*
			TODO check if these possibilities are handled:
			1) got less than a full header (pre-\r\n)
			2) got a perfectly full header (just before body)
			3) got header and part of body
			4) got perfectly full frame
			5) more than a full message
			6) purposefully or otherwise malformed frames/garbage, not even header received within timeout -> timeout or buffer full
			7) keeping track of number of connections per source IP - if too many with not a header received -> DoS-type attack
		*/
		if fr, err := flowd.ParseFrame(bufr); err != nil {
			if err == io.EOF {
				_, portStr, _ := net.SplitHostPort(ep.Url.Host)
				if portStr != "0" {
					// can listen again since port is same (would have to change zeroconf announce)
					fmt.Println("EOF from network input", ep.LocalPort, "- listening again.")

					// start listening again to allow re-connection
					ep.ListenAgain(func() {
						handleInputEndpoint(ep, cin)
					})
				} else {
					fmt.Println("EOF from network input", ep.LocalPort, "- closing.")
				}
			} else {
				fmt.Println("ERROR: receiving from network input:", err, "Closing.")
			}
			ep.Conn.Close()
			return
		} else { // parsed fine
			if debug {
				fmt.Println("received frame type", fr.Type, "and data type", fr.BodyType, "for port", fr.Port, "with body:", (string)(fr.Body)) //TODO difference between this and string(fr.Body) ?
			}

			// check frame Port header field if it matches the name of this input endpoint
			if ep.LocalPort != fr.Port {
				fmt.Println("net in: WARNING: frame for wrong/undeclared port", fr.Port, "- expected:", ep.LocalPort, " - discarding.")
				// discard frame
				continue
			}
			// forward frame to component
			if err := fr.Marshal(cin); err != nil {
				fmt.Println("net in: WARNING: could not marshal received frame into component STDIN - discarding.")
			}

			// status message
			if debug {
				//fmt.Println("STDIN wrote", nPrev+n, "bytes from", ep.Conn.RemoteAddr(), "to component stdin")
				fmt.Println("STDIN wrote", countr.Count()-oldCount, "bytes from", ep.Conn.RemoteAddr(), "to component stdin")
			} else if !quiet {
				fmt.Println("in xfer", countr.Count()-oldCount, "bytes on", fr.Port, "from", ep.Conn.RemoteAddr())
			}
			oldCount = countr.Count()
		}
	}
}

func handleOutputEndpoint(outEndpoints outputEndpoints, cout io.ReadCloser) {
	countr := datacounter.NewReaderCounter(cout)
	bufr := bufio.NewReader(countr)
	var countw *datacounter.WriterCounter
	var localPort string
	for {
		if frame, err := flowd.ParseFrame(bufr); err != nil {
			if err == io.EOF {
				fmt.Println("EOF from component stdout. Exiting.")
			} else {
				fmt.Println("ERROR parsing frame from component stdout:", err, "- Exiting.")
			}
			outEndpoints.Close()
			return
		} else { // frame complete now
			if debug {
				fmt.Println("STDOUT received frame type", frame.Type, "and data type", frame.BodyType, "for port", frame.Port, "with body:", (string)(frame.Body)) //TODO what is difference between this and string(frame.Body) ?
			}

			// write out to network
			if e, exists := outEndpoints[frame.Port]; exists {

				// rewrite frame.Port to match the other side's input port name
				// NOTE: This makes multiple input ports possible
				localPort = frame.Port //TODO any way to do without this variable?
				frame.Port = e.RemotePort

				// marshal and write
				countw = datacounter.NewWriterCounter(e.Conn) //TODO could save this WriterCounter in Endpoint struct
				if err := frame.Marshal(countw); err != nil {
					fmt.Println("net out: ERROR: marshalling frame into output endpoint", localPort, ":", err.Error(), "- closing.")
					outEndpoints[localPort].Close()
					//TODO return as well = close down all output operations or allow one output to fail?
				}

				// status message
				if debug {
					fmt.Println("net out wrote", countw.Count(), "bytes to port", localPort, "=", outEndpoints[localPort].Conn.RemoteAddr(), "with body:", string(frame.Body))
				} else if !quiet {
					fmt.Println("out xfer", countw.Count(), "bytes to", localPort, "=", outEndpoints[localPort].Conn.RemoteAddr())
				}
			} else {
				fmt.Printf("net out: ERROR: component tried sending to undeclared port %s. Exiting.\n", localPort)
				outEndpoints.Close()
				return
			}
		}
	}
}

// Extract local port name in URL/Endpoint.Fragment
// format: [local]>[remote]
//TODO this function will make problems with array ports -> use regex like in libflowd.ParseEndpointURL
func fragmentLocal(fragment string) string {
	if parts := strings.SplitN(fragment, ">", 2); len(parts) == 2 {
		return parts[0]
	} else {
		return fragment
	}
}

// Extract remote port name in URL/Endpoint.Fragment
// format: [local]>[remote]
//TODO this function will make problems with array ports -> use regex like in libflowd.ParseEndpointURL
func fragmentRemote(fragment string) string {
	if parts := strings.SplitN(fragment, ">", 2); len(parts) == 2 {
		return parts[1]
	} else {
		return fragment
	}
}
