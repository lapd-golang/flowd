package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/ERnsTL/flowd/libflowd"
	"github.com/ERnsTL/flowd/libunixfbp"
)

/*
TODO from Github issue #91:
- With array ports, then output port does not have to be specified, but is always OUT, but on different array indices.
- IIP format could be -field MyHeaderField A B C, meaning =A IPs go to OUT[0], =B go to OUT[1] and =C go to OUT[2].
- What is the advantage of that? Does it have one? Simpler flag parsing. Anything else?
*/

// rule keeps a rule entry; used during flag parsing
type rule struct {
	isEquals    bool //TODO this does not scale; refactor to const type if more condition types are added
	isHasPrefix bool
	value       string
	targetport  string
}

var (
	rules = []rule{}
)

func main() {
	// flag variables
	var field, present, missing, nomatchPort string
	var equals equalsFlag
	var hasprefix prefixFlag
	var to toFlag
	// get configuration from arguments = Unix IIP
	unixfbp.DefFlags()
	flag.StringVar(&field, "field", "", "header field to inspect")
	flag.StringVar(&present, "present", "", "outport for packets with header field present")
	flag.StringVar(&missing, "missing", "NOMATCH", "outport for packets with header field missing")
	flag.StringVar(&nomatchPort, "nomatch", "NOMATCH", "outport for unmatched packets")
	flag.Var(&hasprefix, "hasprefix", "matching on prefix in header field value")
	flag.Var(&equals, "equals", "matching equal value of header field")
	flag.Var(&to, "to", "outport for matching packets")
	flag.Parse()

	// check flags
	if len(rules) > 0 && rules[len(rules)-1].value == "" {
		fmt.Fprintln(os.Stderr, "ERROR:", getLastRuleType(), "without following -to, but both required")
		printUsage()
		flag.PrintDefaults() // prints to STDERR
		os.Exit(2)
	}
	//TODO allow both -present and detailed conditions -> if len(rules) > 0 then append present-ruleFunc as last when no details condition matched
	if (present != "" && len(rules) != 0) || (present == "" && len(rules) == 0) {
		fmt.Fprintln(os.Stderr, "ERROR: either -present or specific condition expected")
		printUsage()
		flag.PrintDefaults() // prints to STDERR
		os.Exit(2)
	}
	if field == "" {
		fmt.Fprintln(os.Stderr, "ERROR: -field missing")
		printUsage()
		flag.PrintDefaults() // prints to STDERR
		os.Exit(2)
	}
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "ERROR: unexpected free argument encountered")
		printUsage()
		flag.PrintDefaults() // prints to STDERR
		os.Exit(2)
	}

	if !unixfbp.Quiet {
		fmt.Fprintln(os.Stderr, "starting up")
	}

	// generate frame matchers
	//TODO possible optimization regarding *string return value
	var ruleFuncs []ruleMatcher
	// header value missing
	if missing != "" {
		ruleFuncs = append(ruleFuncs, func(value *string) *string {
			if value == nil {
				return &missing
			}
			// no match
			return nil
		})
	}
	// header value present
	if present != "" {
		ruleFuncs = append(ruleFuncs, func(value *string) *string {
			if value != nil {
				return &present
			}
			// no match
			return nil
		})
	}
	// regular equality rules
	if len(rules) > 0 {
		if unixfbp.Debug {
			fmt.Fprintln(os.Stderr, "routing table:")
		}
		for _, rule := range rules {
			// make copies so that func gets local copy, otherwise all rules would be the same
			//TODO check if this is actually so...
			matchValueCopy := rule.value
			targetPortCopy := rule.targetport
			// append rule function depending on rule type
			if rule.isEquals {
				if unixfbp.Debug {
					fmt.Fprintf(os.Stderr, "\tif %s equals %s, forward to %s\n", field, matchValueCopy, targetPortCopy)
				}
				ruleFuncs = append(ruleFuncs, func(value *string) *string {
					if value == nil {
						// not responsible
						return nil
					}
					if *value == matchValueCopy {
						return &targetPortCopy
					}
					// no match
					return nil
				})
			} else if rule.isHasPrefix {
				if unixfbp.Debug {
					fmt.Fprintf(os.Stderr, "\tif %s has prefix %s, forward to %s\n", field, matchValueCopy, targetPortCopy)
				}
				ruleFuncs = append(ruleFuncs, func(value *string) *string {
					if value == nil {
						// not responsible
						return nil
					}
					if strings.HasPrefix(*value, matchValueCopy) {
						return &targetPortCopy
					}
					// no match
					return nil
				})
			} else {
				fmt.Fprintln(os.Stderr, "ERROR: unknown rule type - exiting.")
				os.Exit(2)
			}
		}
		if unixfbp.Debug {
			fmt.Fprintf(os.Stderr, "\tif %s missing, forward to %s\n", field, nomatchPort)
		}
	}
	// default catch-all rule
	ruleFuncs = append(ruleFuncs, func(value *string) *string {
		return &nomatchPort
	})
	// empty rules list
	rules = nil

	// header field getter
	var fieldGetter fieldGetter
	switch field {
	case "Type":
		fieldGetter = func(frame *flowd.Frame) *string {
			return &frame.Type
		}
	case "BodyType":
		fieldGetter = func(frame *flowd.Frame) *string {
			return &frame.BodyType
		}
	default:
		fieldGetter = func(frame *flowd.Frame) *string {
			if frame.Extensions == nil {
				// nothing there
				return nil
			}
			if value, exists := frame.Extensions[field]; exists {
				return &value
			}
			// field missing
			return nil
		}
	}

	// connect to FBP network
	var err error
	netin, _, err := unixfbp.OpenInPort("IN")
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(2)
	}
	netout, _, err := unixfbp.OpenOutPort("OUT")
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(2)
	}
	defer netout.Flush()

	// prepare variables
	var frame *flowd.Frame
	var fieldValue *string

	// main work loop
	//TODO make many outputs configurable by -debug
nextframe:
	for {
		// read frame
		frame, err = flowd.Deserialize(netin)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}

		// check for closed input port
		if frame.Type == "control" && frame.BodyType == "PortClose" && frame.Port == "IN" {
			// shut down operations
			fmt.Fprintln(os.Stderr, "input port closed - exiting.")
			break
		}

		// get field value
		/*
			if frame.Extensions != nil {
				if value, found := frame.Extensions["Myfield"]; found {
					fmt.Fprintf(os.Stderr, "in frame directly: field has value %s\n", value)
				} else {
					fmt.Fprintln(os.Stderr, "in frame directly: field not found")
				}
			} else {
				fmt.Fprintln(os.Stderr, "in frame directly: field not found")
			}
		*/
		fieldValue = fieldGetter(frame)
		if unixfbp.Debug {
			if fieldValue != nil {
				fmt.Fprintf(os.Stderr, "field %s has value %s\n", field, *fieldValue)
			} else {
				fmt.Fprintf(os.Stderr, "field %s has value %v\n", field, fieldValue)
			}
		}

		// check which rule applies
		for _, ruleFunc := range ruleFuncs {
			if targetPort := ruleFunc(fieldValue); targetPort != nil {
				// rule applies, forward frame to returned port
				if unixfbp.Debug {
					fmt.Fprintf(os.Stderr, "forwarding to port %s\n", *targetPort)
				}
				frame.Port = *targetPort
				if err = frame.Serialize(netout); err != nil {
					fmt.Fprintln(os.Stderr, "ERROR: marshaling frame:", err)
				}
				if netin.Buffered() == 0 {
					if err = netout.Flush(); err != nil {
						fmt.Fprintln(os.Stderr, "ERROR: flushing netout:", err)
					}
				}
				// done with this frame
				continue nextframe
			}
		}

		// no rule matched, not even final catch-all rule
		fmt.Fprintln(os.Stderr, "ERROR: no rule matched, should never be reached - exiting.")
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Arguments: [-field] [-missing] [-present] {[-equals|-hasprefix] [-to]}...")
}

// flag acceptors of ( -equals [value] or -hasprefix [prefix] ) and -to [output-port] couples
// NOTE: need separate types in order to know from which flag the value came from
type equalsFlag struct{}

func (f *equalsFlag) String() string {
	// return default value
	return ""
}

func (f *equalsFlag) Set(value string) error {
	if !lastRuleIsComplete() {
		// another detailed condition was previously given, but -to expected, not another conditition
		return fmt.Errorf("-equals follows another condition, expecting -to")
	}

	// make new rule; target port will be filled by following -to flag
	rules = append(rules, rule{
		isEquals: true,
		value:    value,
	})
	return nil
}

type prefixFlag struct{}

func (f *prefixFlag) String() string {
	// return default value
	return ""
}

func (f *prefixFlag) Set(value string) error {
	if !lastRuleIsComplete() {
		// another detailed condition was previously given, but -to expected, not another conditition
		return fmt.Errorf("-hasprefix follows another condition, expecting -to")
	}

	// make new rule; target port will be filled by following -to flag
	rules = append(rules, rule{
		isHasPrefix: true,
		value:       value,
	})
	return nil
}

type toFlag struct{}

func (f *toFlag) String() string {
	// return default value
	return ""
}

func (f *toFlag) Set(value string) error {
	if lastRuleIsComplete() {
		// no value from previous -equals flag
		return fmt.Errorf("-to without preceding -equals or -hasprefix condition")
	}

	// save target port
	rules[len(rules)-1].targetport = value
	return nil
}

// frame field retrieval and rule matcher function definitions
type ruleMatcher func(*string) *string
type fieldGetter func(*flowd.Frame) *string

// getLastRuleType is a shorthand for a string representation of the last rule's type
func getLastRuleType() string {
	if rules[len(rules)-1].isEquals {
		return "-equals"
	} else if rules[len(rules)-1].isHasPrefix {
		return "-hasprefix"
	}
	return "ERROR: unknown last rule type"
}

// lastRuleIsComplete is used during flag parsing of detailed-condition and -to flag pairs;
// returns if -to flag, thus a target port, is already given = complete rule or -to expected
func lastRuleIsComplete() bool {
	// do not have last rule, but is complete = we are starting a new rule
	if len(rules) == 0 {
		return true
	}

	if rules[len(rules)-1].targetport != "" {
		return true
	}
	return false
}
