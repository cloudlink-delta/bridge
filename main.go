package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/cloudlink-delta/bridge/server"
	"github.com/cloudlink-delta/duplex"
	"github.com/pion/webrtc/v3"
)

func main() {

	// CLI flags
	configFile := flag.String("config", "", "Path to JSON configuration file")
	designationFlag := flag.String("designation", "", "Globally unique designation (required)")

	// Duplex lib flags
	enablePinger := flag.Bool("enable-pinger", false, "Enable ping/pong keepalive")
	pingInterval := flag.Int64("ping-interval", 5000, "Ping/pong interval (in milliseconds)")
	veryVerbose := flag.Bool("very-verbose", false, "Enable very verbose logging (this will absolutely thrash your terminal)")
	enableSecure := flag.Bool("session-secure", true, "Enable secure session server connections (required if session-hostname is set)")
	sessionServerPort := flag.Int("session-port", 443, "Port where the session server is listening (required if session-hostname is set)")
	sessionServerHostname := flag.String("session-hostname", "", "Hostname where the session server is listening")
	iceServersFlag := flag.String("ice-servers", "", "JSON-encoded array of ICE servers")

	// Discovery server flags
	enableMOTD := flag.Bool("enable-motd", true, "Enable message-of-the-day")
	motdMessage := flag.String("motd-message", "Welcome to the CloudLink Bridge!", "Message-of-the-day")
	serveIPs := flag.Bool("serve-ips", true, "Serve IP addresses to legacy CloudLink clients")
	maxRooms := flag.Int("max-rooms", 100, "Maximum number of rooms")
	maxClients := flag.Int("max-clients", 1000, "Maximum number of clients")
	forceSet := flag.Bool("force-set", true, "Force the use of the `set` ulist mode for legacy CloudLink clients")
	address := flag.String("address", "127.0.0.1:3000", "Legacy CloudLink listener address")

	// Parse command-line flags
	flag.Usage = func() {
		log.Println("Usage: bridge [options]")
		log.Println("Options:")
		flag.PrintDefaults()
	}
	flag.Parse()

	// Defaults
	designation := ""
	serverCfg := server.Config{
		Enable_MOTD:        true,
		MOTD_Message:       "Welcome to the CloudLink Bridge!",
		Serve_IP_Addresses: true,
		Maximum_Rooms:      100,
		Maximum_Clients:    1000,
		Force_Set:          true,
		Address:            "127.0.0.1:3000",
	}
	duplexCfg := duplex.Config{
		PingInterval: 5000,
		Secure:       true,
		Port:         443,
	}

	// Parser checks
	sessionHostnameProvided := false
	sessionSecureProvided := false
	sessionPortProvided := false

	// If config file is provided, load it
	if *configFile != "" {
		data, err := os.ReadFile(*configFile)
		if err != nil {
			log.Fatalf("Failed to read config file: %v", err)
		}

		var fileCfg struct {
			Designation      *string            `json:"designation"`
			EnableMOTD       *bool              `json:"enable_motd"`
			MOTDMessage      *string            `json:"motd_message"`
			ServeIPAddresses *bool              `json:"serve_ip_addresses"`
			MaximumRooms     *int               `json:"maximum_rooms"`
			MaximumClients   *int               `json:"maximum_clients"`
			ForceSet         *bool              `json:"force_set"`
			Address          *string            `json:"address"`
			ICEServers       []webrtc.ICEServer `json:"ice_servers"`
			SessionHostname  *string            `json:"session_hostname"`
			SessionSecure    *bool              `json:"session_secure"`
			SessionPort      *int               `json:"session_port"`
		}

		if err := json.Unmarshal(data, &fileCfg); err != nil {
			log.Fatalf("Failed to parse config file: %v", err)
		}

		if fileCfg.Designation != nil {
			designation = *fileCfg.Designation
		}
		if fileCfg.EnableMOTD != nil {
			serverCfg.Enable_MOTD = *fileCfg.EnableMOTD
		}
		if fileCfg.MOTDMessage != nil {
			serverCfg.MOTD_Message = *fileCfg.MOTDMessage
		}
		if fileCfg.ServeIPAddresses != nil {
			serverCfg.Serve_IP_Addresses = *fileCfg.ServeIPAddresses
		}
		if fileCfg.MaximumRooms != nil {
			serverCfg.Maximum_Rooms = uint(*fileCfg.MaximumRooms)
		}
		if fileCfg.MaximumClients != nil {
			serverCfg.Maximum_Clients = uint(*fileCfg.MaximumClients)
		}
		if fileCfg.ForceSet != nil {
			serverCfg.Force_Set = *fileCfg.ForceSet
		}
		if fileCfg.Address != nil {
			serverCfg.Address = *fileCfg.Address
		}
		if fileCfg.ICEServers != nil {
			duplexCfg.ICEServers = fileCfg.ICEServers
		}
		if fileCfg.SessionHostname != nil {
			duplexCfg.Hostname = *fileCfg.SessionHostname
			sessionHostnameProvided = true
		}
		if fileCfg.SessionSecure != nil {
			duplexCfg.Secure = *fileCfg.SessionSecure
			sessionSecureProvided = true
		}
		if fileCfg.SessionPort != nil {
			duplexCfg.Port = *fileCfg.SessionPort
			sessionPortProvided = true
		}
	}

	// Override with explicitly set command-line flags
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "designation":
			designation = *designationFlag
		case "enable-motd":
			serverCfg.Enable_MOTD = *enableMOTD
		case "motd-message":
			serverCfg.MOTD_Message = *motdMessage
		case "serve-ips":
			serverCfg.Serve_IP_Addresses = *serveIPs
		case "max-rooms":
			serverCfg.Maximum_Rooms = uint(*maxRooms)
		case "max-clients":
			serverCfg.Maximum_Clients = uint(*maxClients)
		case "force-set":
			serverCfg.Force_Set = *forceSet
		case "address":
			serverCfg.Address = *address
		case "enable-pinger":
			duplexCfg.EnablePinger = *enablePinger
		case "ping-interval":
			duplexCfg.PingInterval = *pingInterval
		case "very-verbose":
			duplexCfg.VeryVerbose = *veryVerbose
		case "session-secure":
			duplexCfg.Secure = *enableSecure
			sessionSecureProvided = true
		case "session-port":
			duplexCfg.Port = *sessionServerPort
			sessionPortProvided = true
		case "session-hostname":
			duplexCfg.Hostname = *sessionServerHostname
			sessionHostnameProvided = true
		case "ice-servers":
			var iceServers []webrtc.ICEServer
			if err := json.Unmarshal([]byte(*iceServersFlag), &iceServers); err != nil {
				log.Fatalf("Failed to parse ice-servers flag: %v", err)
			}
			duplexCfg.ICEServers = iceServers
		}
	})

	// Verify loaded configuration
	if designation == "" {
		log.Fatal("A designation is required. Please provide it via -designation or in a config.json file. See --help for more information.")
	}
	if sessionHostnameProvided {
		if !sessionSecureProvided || !sessionPortProvided {
			log.Fatal("When session-hostname is provided, both session-secure and session-port must also be provided explicitly. See --help for more information.")
		}
	}

	// Initialize the bridge server
	instance := server.New(designation, &serverCfg, &duplexCfg)

	// Graceful shutdown handler
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		instance.Close <- true
		<-instance.Done
		os.Exit(1)
	}()

	// Run the server
	instance.Run()
}
