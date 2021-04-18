/*
 * Toby LTE Modem Manager
 * Written by JBG 20210418
 */

package main

import (
	"flag"
	"fmt"
	"bufio"
	"io"
	"io/ioutil"
	"strings"
	"regexp"
	"os"
	"os/exec"
	"net"
	"time"
	"container/list"
	"github.com/jacobsa/go-serial/serial"
	"github.com/tatsushid/go-fastping"
)

var ttyAvailable bool
var ttyPort string
var ttyBaud uint
var providerAPN string
var networkInterface string

var NetworkIP string
var GatewayIP string
var PrimaryDNSIP string
var SecondaryDNSIP string

var timeouts uint

var transmitBuffer []byte
var receivedBuffer []byte
var receivedToProcess []byte
var receivedToQueue []byte
var transmitQueue = list.New()
var receiveQueue = list.New()
var receivedBytes uint
var debugMode uint

const OUTPUT_VERBOSE = 1
const OUTPUT_DEBUG = 2

func usage() {
	// Print our command line usage
	fmt.Println(os.Args[0], " usage:")
	flag.PrintDefaults()
	os.Exit(-1)
}

func readStdin(debugMode uint) {
	success, response := false, ""
	stdin := bufio.NewReader(os.Stdin)
	for {
		text, _ := stdin.ReadString('\n')
		text = strings.TrimSuffix(text, "\n")
		if text != "" {
			if debugMode == OUTPUT_DEBUG { fmt.Println("Received STDIN:", text) }
			success, response = makeRequest(text, debugMode)
			fmt.Println("Success:", success, "Response:", response)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func makeRequest(command string, debugMode uint) (bool, string) {
	timeout := time.Now().UnixNano() + (5 * 1000000000)
	completed := false
	success := false
	response := ""

	searchTerm := regexp.MustCompile("^(at|AT)").ReplaceAllLiteralString(command, "")
	searchTerm  = regexp.MustCompile("^.[a-zA-Z]*").FindString(searchTerm)
	if debugMode == OUTPUT_DEBUG { fmt.Printf("Searching for \"%s\"\r\n", searchTerm) }
	transmitQueue.PushBack(command)

	for {
		// Process any responses here
		for receiveQueue.Len() > 0 {
			item := receiveQueue.Front()
			s := fmt.Sprintf("%s", item.Value)
			if debugMode == OUTPUT_DEBUG {
				fmt.Printf("Processing response %v: %s\r\n", receiveQueue.Len(), s)
				fmt.Println("Haystack:", s)
				fmt.Println("Needle:", searchTerm)
			}
			if searchTerm != "" && strings.Contains(s, searchTerm + ": ") {
				response = strings.Replace(s, searchTerm + ": ", "", 1)
			} else if s == "OK" {
				success = true
				completed = true
			} else if strings.Contains(s, "ERROR") {
				response = s
				success = false
				completed = true
			}
			receiveQueue.Remove(item)
		}
		if completed {
			return success, response
		} else if time.Now().UnixNano() > timeout {
			response = "Timed out waiting for response"
			return false, response
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func enforceModemSetting(debugMode uint, modemSetting string, settingFriendlyName string) (bool) {
	success, response := false, ""
	restartRequired := false
	querySetting := strings.Split(modemSetting, "=")[0]
	desiredValue := strings.Split(modemSetting, "=")[1]

	success, response = makeRequest(querySetting + "?", debugMode)
	if !success {
		fmt.Printf("Error polling modem %s: %s\r\nIssuing modem reset and exiting...\r\n", settingFriendlyName, response)
		transmitQueue.PushBack("ATZ")
		time.Sleep(2000 * time.Millisecond)
		os.Exit(-1)
	} else if !strings.Contains(response, desiredValue) {
		fmt.Printf("Modem is in the incorrect %s (%s), should be %s, issuing %s and will restart later...\r\n", settingFriendlyName, response, desiredValue, modemSetting)
		success, response = makeRequest(modemSetting, debugMode)
		if !success {
			fmt.Printf("Error setting modem %s: %s\r\nIssuing modem reset and exiting...\r\n", settingFriendlyName, response)
			transmitQueue.PushBack("ATZ")
			time.Sleep(2000 * time.Millisecond)
			os.Exit(-1)
		}
		restartRequired = true
	} else {
		fmt.Printf("Modem in desired %s (%s), continuing...\r\n", settingFriendlyName, response)
	}
	return restartRequired
}

func runScript(debugMode uint) {
	// Set up variables to reuse
	success, response, restartRequired, nameservers := false, "", false, []byte("")
	_ = success; _ = response; _ = restartRequired; _ = nameservers
	pinger := fastping.NewPinger()
	pinger.OnRecv = func(addr *net.IPAddr, rtt time.Duration) {
		if debugMode == OUTPUT_VERBOSE { fmt.Printf("IP Addr: %s receive, RTT: %v\r\n", addr.String(), rtt) }
		timeouts = 0
	}
	pinger.OnIdle = func() { timeouts++ }
	pinger.MaxRTT = time.Second * 5

	// Make sure AT+UBMCONF=2
	restartRequired = enforceModemSetting(debugMode, "AT+UBMCONF=2", "networking mode") || restartRequired

	// Make sure AT+UUSBCONF=2,"ECM"
	restartRequired = enforceModemSetting(debugMode, "AT+UUSBCONF=2,\"ECM\"", "USB mode") || restartRequired

	// Make sure AT+UWWEBUI=1
	restartRequired = enforceModemSetting(debugMode, "AT+UWWEBUI=1", "web server mode") || restartRequired

	// Restart modem here if required
	if restartRequired {
		fmt.Println("Restarting modem and then program once it returns...")
		transmitQueue.PushBack("AT+CFUN=1,1")
		// Wait for modem to disappear
		modemDisappeared := false
		for {
			if _, err := os.Stat(ttyPort); os.IsNotExist(err) {
				if !modemDisappeared {
					fmt.Println("Modem disappeared, awaiting its return...")
					modemDisappeared = true
				}
			} else if modemDisappeared {
				fmt.Println("Modem reappeared, restarting in 2 seconds...")
				time.Sleep(2000 * time.Millisecond)
				os.Exit(0)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	CONNECT:
	for {
		// Correctly set up our PDP context
		PDPCommands := []string{
			"AT+CMEE=2",
			"AT+CFUN=4",
			"AT+CGDEL=1",
			"AT+CGDEL=2",
			"AT+CGDEL=3",
			"AT+CGDEL=4",
			"AT+CGDEL=5",
			"AT+CGDEL=6",
			"AT+CGDEL=7",
			"AT+CGDEL=8",
			"AT+UCGDFLT=1,\"IP\",\"" + providerAPN + "\"",
			"AT+CFUN=1",
		}
		fmt.Println("Setting up PDP context...")
		for _, s := range PDPCommands {
			success, response = makeRequest(s, debugMode)
			if !success {
				fmt.Printf("Error issuing PDP command %s: %s\r\nIssuing modem reset and exiting...\r\n", s, response)
				transmitQueue.PushBack("ATZ")
				time.Sleep(2000 * time.Millisecond)
				os.Exit(-1)
			}
		}
		time.Sleep(2000 * time.Millisecond)

		// Make sure we are connected to the network
		fmt.Println("Waiting for connection to mobile network to come up...")
		for i := 20; i > 0; i-- {
			success, response = makeRequest("AT+CREG?", debugMode)
			if !success {
				fmt.Printf("Error checking network status: %s\r\nIssuing modem reset and exiting...\r\n", response)
				transmitQueue.PushBack("ATZ")
				time.Sleep(2000 * time.Millisecond)
				os.Exit(-1)
			}
			if strings.Split(response, ",")[1] == "1" || strings.Split(response, ",")[1] == "5" {
				success = true
				break
			} else {
				success = false
			}
			time.Sleep(500 * time.Millisecond)
		}
		if !success {
			fmt.Printf("Network did not come up, AT+CREG network status: %s\r\nIssuing modem reset and exiting...\r\n", response)
			transmitQueue.PushBack("ATZ")
			time.Sleep(2000 * time.Millisecond)
			os.Exit(-1)
		}

		// Bind our PDP to our default session and grab network information
		PDPCommands = []string{
			"AT+UPSD=0,100,4",
			"AT+UPSD=0,0,0",
			"AT+UPSDA=0,3",
		}
		fmt.Println("Assigning PDP to default context...")
		for _, s := range PDPCommands {
			success, response = makeRequest(s, debugMode)
			if !success {
				fmt.Printf("Error issuing PDP command %s: %s\r\nIssuing modem reset and exiting...\r\n", s, response)
				transmitQueue.PushBack("ATZ")
				time.Sleep(2000 * time.Millisecond)
				os.Exit(-1)
			}
		}
		time.Sleep(1000 * time.Millisecond)

		// Grab our networking configuration
		fmt.Println("Finding networking configuration...")
		success, response = makeRequest("AT+CGCONTRDP", debugMode)
		if !success {
			fmt.Printf("Error getting modem IP: %s\r\nIssuing modem reset and exiting...\r\n", response)
			transmitQueue.PushBack("ATZ")
			time.Sleep(2000 * time.Millisecond)
			os.Exit(-1)
		}
		NetworkInformation := strings.Split(response, ",")
		success, response = makeRequest("AT+UIPADDR=", debugMode)
		if !success {
			fmt.Printf("Error getting modem gateway IP: %s\r\nIssuing modem reset and exiting...\r\n", response)
			transmitQueue.PushBack("ATZ")
			time.Sleep(2000 * time.Millisecond)
			os.Exit(-1)
		}
		GatewayInformation := strings.Split(response, ",")

		NetworkIP = NetworkInformation[4][1 : len(NetworkInformation[4]) - 1]
		PrimaryDNSIP = NetworkInformation[5][1 : len(NetworkInformation[5]) - 1]
		SecondaryDNSIP = NetworkInformation[6][1 : len(NetworkInformation[6]) - 1]
		GatewayIP = GatewayInformation[2][1 : len(GatewayInformation[2]) - 1]
		pinger.Source(NetworkIP)
		pinger.AddIP(PrimaryDNSIP)
		fmt.Printf("Network IP: %s\r\nPrimary DNS: %s\r\nSecondary DNS: %s\r\nGateway IP: %s\r\n", NetworkIP, PrimaryDNSIP, SecondaryDNSIP, GatewayIP)

		// Bring up wwan0 interface
		fmt.Printf("Bringing up %s...\r\n", networkInterface)
		err := exec.Command("/usr/bin/env", "ifconfig", networkInterface, NetworkIP, "pointopoint", GatewayIP).Run()
		if err != nil {
			fmt.Printf("Error bringing up network interface: %s\r\nIssuing modem reset and exiting...\r\n", err)
			transmitQueue.PushBack("ATZ")
			time.Sleep(2000 * time.Millisecond)
			os.Exit(-1)
		}
		err = exec.Command("/usr/bin/env", "route", "add", "default", "gw", GatewayIP, networkInterface).Run()
		if err != nil {
			fmt.Printf("Error setting network route: %s\r\nIssuing modem reset and exiting...\r\n", err)
			transmitQueue.PushBack("ATZ")
			time.Sleep(2000 * time.Millisecond)
			os.Exit(-1)
		}
		nameservers = []byte("nameserver " + PrimaryDNSIP + "\nnameserver " + SecondaryDNSIP + "\n")
		err = ioutil.WriteFile("/etc/resolv.conf", nameservers, 0644)
		if err != nil {
			fmt.Printf("Error writing resolv.conf: %s\r\nIssuing modem reset and exiting...\r\n", err)
			transmitQueue.PushBack("ATZ")
			time.Sleep(2000 * time.Millisecond)
			os.Exit(-1)
		}

		// Check connectivity by periodically pinging PrimaryDNSIP
		timeouts = 0
		pinger.RunLoop()
		for {
			if timeouts >= 3 {
				fmt.Printf("Error no connectivity after %v pings, attempting to reconnect...\r\n", timeouts)
				pinger.Stop()
				pinger.RemoveIP(PrimaryDNSIP)
				continue CONNECT
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func main() {
	// Main program starts here
	fmt.Println("Toby LTE Modem Manager")

	// Collect our command line options
	port := flag.String("d", "", "serial port for modem (/dev/ttyUSB0, etc)")
	baud := flag.Uint("b", 115200, "Baud rate")
	apn := flag.String("a", "", "APN (Access Point Name) for mobile provider")
	iface := flag.String("i", "", "Network interface to assign IP to (ie. wwan0)")
	debug := flag.Bool("vv", false, "Debug output (warning: noisy!)")
	verbose := flag.Bool("v", false, "Verbose output")
	flag.Parse()

	if *port == "" {
		fmt.Println("Must specify port")
		usage()
	}

	if *apn == "" {
		fmt.Println("Must specify APN")
		usage()
	}

	if *iface == "" {
		fmt.Println("Must specify network interface")
		usage()
	}

	ttyAvailable = false
	ttyPort = *port
	ttyBaud = *baud
	providerAPN = *apn
	networkInterface = *iface
	
	if *debug { debugMode = OUTPUT_DEBUG } else if *verbose { debugMode = OUTPUT_VERBOSE }

	// Set up some common buffers for use outside of scope
	transmitBuffer := make([]byte, 255); _ = transmitBuffer
	receivedBuffer := make([]byte, 255); _ = receivedBuffer
	receivedToProcess := make([]byte, 255); _ = receivedToProcess
	receivedToQueue := make([]byte, 255); _ = receivedToQueue
	receivedBytes = 0

	// Set up our serial port
	options := serial.OpenOptions{
		PortName:               ttyPort,
		BaudRate:               ttyBaud,
		DataBits:               8,
		StopBits:               1,
		MinimumReadSize:        0,
		InterCharacterTimeout:  200,
	}

	// Open serial port
	serialPort, err := serial.Open(options); _ = serialPort
	if err != nil {
		fmt.Println("Error opening serial port:", err)
		os.Exit(-1)
	} else {
		ttyAvailable = true
		// defer serialPort.Close()
	}

	// Set up a regular tasks
	go runScript(debugMode)
	if debugMode == OUTPUT_DEBUG { go readStdin(debugMode) }

	// Main processing loop here
	MAIN:
	for {
		// Check our serial port is still valid
		if _, err := os.Stat(ttyPort); os.IsNotExist(err) {
			serialPort.Close()
			ttyAvailable = false
		}
		if ttyAvailable {
			// Read any received bytes here
			n, err := serialPort.Read(receivedBuffer)
			if err != nil {
				if err != io.EOF {
					fmt.Println("Error reading from serial port:", err)
					ttyAvailable = false
					continue MAIN
				}
			} else { receivedBuffer = receivedBuffer[:n] }

			for i := 0; i < n; i++ {
				receivedChar := string(receivedBuffer[i])
				if receivedChar == string(byte(10)) { receivedChar = "\\n" } else if receivedChar == string(byte(13)) { receivedChar = "\\r" }

				receivedToProcess[receivedBytes] = receivedBuffer[i]
				receivedBytes++

				if receivedBytes >= 2 {
					if receivedToProcess[receivedBytes - 1] == byte(10) && receivedToProcess[receivedBytes - 2] == byte(13) {
						// New command to process
						receivedToQueue = make([]byte, receivedBytes)
						for j := range receivedToQueue { receivedToQueue[j] = byte(0) }
						receivedToQueue = receivedToProcess[:receivedBytes - 2]
						if fmt.Sprintf("%s", string(receivedToQueue)) != "" {
							if debugMode == OUTPUT_VERBOSE { fmt.Printf("RX: %s\r\n", string(receivedToQueue)) }
							receiveQueue.PushBack(string(receivedToQueue))
						}
						receivedBytes = 0
					}
				}
			}

			// Transmit any pending messages
			for transmitQueue.Len() > 0 {
				s := transmitQueue.Front()
				transmitBytes := len(fmt.Sprintf("%s\r\n", s.Value))
				transmitBuffer = make([]byte, transmitBytes)
				for i := range transmitBuffer { transmitBuffer[i] = byte(0) }
				copy(transmitBuffer, fmt.Sprintf("%s\r\n", s.Value))
				if debugMode == OUTPUT_DEBUG { fmt.Printf("Attempting to transmit %v bytes: %s\n", transmitBytes, string(transmitBuffer)) }
				count, err := serialPort.Write(transmitBuffer)
				if err != nil {
					fmt.Println("Error writing to serial port:", err)
					continue MAIN
				} else if count != transmitBytes {
					fmt.Printf("Error partial write, wrote %v out of %v bytes: %s", count, transmitBytes, string(transmitBuffer))
				} else {
					if debugMode == OUTPUT_VERBOSE { fmt.Printf("TX: %s", string(transmitBuffer)) }
				}
				transmitQueue.Remove(s)
			}

			// Sleep to avoid smacking the CPU
			time.Sleep(10 * time.Millisecond)
		} else {
			/* TODO

			This section is awaiting a response to https://github.com/jacobsa/go-serial/issues/51

			// Attempt to reconnect to serial port with a delay
			fmt.Println("Attempting to reopen serial port")
			serialPort.Close()
			time.Sleep(1000 * time.Millisecond)
			options := serial.OpenOptions{
				PortName:               ttyPort,
				BaudRate:               ttyBaud,
				DataBits:               8,
				StopBits:               1,
				MinimumReadSize:        0,
				InterCharacterTimeout:  200,
			}
			serialPort, err := serial.Open(options); _ = serialPort
			if err != nil {
				fmt.Println("Serial port currently not available:", err)
				fmt.Println("Will retry in 5 seconds")
				time.Sleep(5000 * time.Millisecond)
			} else {
				time.Sleep(1000 * time.Millisecond)
				ttyAvailable = true
			}

			Workaround is to depend on systemd to restart the script:
		*/
			fmt.Println("Serial port is no longer available, restarting in 10 seconds...")
			time.Sleep(10000 * time.Millisecond)
			os.Exit(-1)
		}
	}
}