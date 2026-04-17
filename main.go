package main

import (
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cloudlink-delta/bridge/server"
	"github.com/cloudlink-delta/duplex"
	"github.com/pion/webrtc/v3"
	"github.com/rs/zerolog"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

func main() {

	// CLI flags
	pflag.Int("log-level", (int)(zerolog.InfoLevel), "Logging level to use. Acceptable values range from -1 to 7. (default: 1 \"Info\")")
	pflag.String("config", "", "Path to JSON configuration file")
	pflag.String("designation", "", "Globally unique designation (required)")
	pflag.Bool("standalone", false, "Run in standalone mode (Only run the classic Clients server)")

	// Duplex lib flags
	pflag.Bool("enable-pinger", false, "Enable ping/pong keepalive")
	pflag.Int64("ping-interval", 5000, "Ping/pong interval (in milliseconds)")
	pflag.Bool("session-secure", true, "Enable secure session server connections (required if session-hostname is set)")
	pflag.Int("session-port", 443, "Port where the session server is listening (required if session-hostname is set)")
	pflag.String("session-hostname", "", "Hostname where the session server is listening")
	pflag.String("ice-servers", "", "JSON-encoded array of ICE servers")

	// Discovery server flags
	pflag.Bool("enable-motd", true, "Enable message-of-the-day")
	pflag.String("motd-message", "Welcome to the CloudLink Bridge!", "Message-of-the-day")
	pflag.Bool("serve-ips", true, "Serve IP addresses to legacy CloudLink clients")
	pflag.Int("max-rooms", 100, "Maximum number of rooms")
	pflag.Int("max-clients", 1000, "Maximum number of clients")
	pflag.Bool("force-set", true, "Force the use of the `set` ulist mode for legacy CloudLink clients")
	pflag.String("address", "127.0.0.1:3000", "Legacy CloudLink listener address")
	pflag.Bool("enable-rate-limit", true, "Enable rate limiting")
	pflag.Int("rate-limit-burst", 50, "Maximum number of messages per interval for rate limiting")
	pflag.Duration("rate-limit-interval", time.Second, "Interval for rate limiting")
	pflag.Bool("kick-on-rate-limit", false, "Kick clients that exceed the rate limit. Otherwise, packets are dropped.")

	// Parse command-line flags
	pflag.Usage = func() {
		log.Println("Usage: bridge [options]")
		log.Println("Options:")
		pflag.PrintDefaults()
	}
	pflag.Parse()

	// Bind flags to viper
	viper.BindPFlag("log_level", pflag.Lookup("log-level"))
	viper.BindPFlag("config", pflag.Lookup("config"))
	viper.BindPFlag("designation", pflag.Lookup("designation"))
	viper.BindPFlag("standalone_mode", pflag.Lookup("standalone"))
	viper.BindPFlag("enable_pinger", pflag.Lookup("enable-pinger"))
	viper.BindPFlag("ping_interval", pflag.Lookup("ping-interval"))
	viper.BindPFlag("session_secure", pflag.Lookup("session-secure"))
	viper.BindPFlag("session_port", pflag.Lookup("session-port"))
	viper.BindPFlag("session_hostname", pflag.Lookup("session-hostname"))
	viper.BindPFlag("ice_servers_flag", pflag.Lookup("ice-servers"))
	viper.BindPFlag("enable_motd", pflag.Lookup("enable-motd"))
	viper.BindPFlag("motd_message", pflag.Lookup("motd-message"))
	viper.BindPFlag("serve_ip_addresses", pflag.Lookup("serve-ips"))
	viper.BindPFlag("maximum_rooms", pflag.Lookup("max-rooms"))
	viper.BindPFlag("maximum_clients", pflag.Lookup("max-clients"))
	viper.BindPFlag("force_set", pflag.Lookup("force-set"))
	viper.BindPFlag("address", pflag.Lookup("address"))
	viper.BindPFlag("enable_rate_limit", pflag.Lookup("enable-rate-limit"))
	viper.BindPFlag("rate_limit_burst", pflag.Lookup("rate-limit-burst"))
	viper.BindPFlag("rate_limit_interval", pflag.Lookup("rate-limit-interval"))
	viper.BindPFlag("kick_on_rate_limit", pflag.Lookup("kick-on-rate-limit"))

	// If config file is provided, load it
	if cfgFile := viper.GetString("config"); cfgFile != "" {
		viper.SetConfigFile(cfgFile)
		if err := viper.ReadInConfig(); err != nil {
			log.Fatalf("Failed to read config file: %v", err)
		}
	}

	logging_level := zerolog.Level(viper.GetInt("log_level"))
	designation := viper.GetString("designation")
	standaloneMode := viper.GetBool("standalone_mode")

	serverCfg := server.Config{
		Designation:         designation,
		Enable_MOTD:         viper.GetBool("enable_motd"),
		MOTD_Message:        viper.GetString("motd_message"),
		Serve_IP_Addresses:  viper.GetBool("serve_ip_addresses"),
		Maximum_Rooms:       uint(viper.GetInt("maximum_rooms")),
		Maximum_Clients:     uint(viper.GetInt("maximum_clients")),
		Force_Set:           viper.GetBool("force_set"),
		Address:             viper.GetString("address"),
		Enable_Rate_Limit:   viper.GetBool("enable_rate_limit"),
		Rate_Limit_Burst:    viper.GetInt("rate_limit_burst"),
		Rate_Limit_Interval: viper.GetDuration("rate_limit_interval"),
		Kick_On_Rate_Limit:  viper.GetBool("kick_on_rate_limit"),
		Standalone_Mode:     standaloneMode,
		Log_Level:           logging_level,
	}

	duplexCfg := duplex.Config{
		LogLevel:     logging_level,
		PingInterval: viper.GetInt64("ping_interval"),
		EnablePinger: viper.GetBool("enable_pinger"),
		Secure:       viper.GetBool("session_secure"),
		Port:         viper.GetInt("session_port"),
	}

	sessionHostnameProvided := pflag.CommandLine.Changed("session-hostname") || viper.IsSet("session_hostname")
	sessionSecureProvided := pflag.CommandLine.Changed("session-secure") || viper.IsSet("session_secure")
	sessionPortProvided := pflag.CommandLine.Changed("session-port") || viper.IsSet("session_port")

	if sessionHostnameProvided {
		duplexCfg.Hostname = viper.GetString("session_hostname")
	}

	var iceServers []webrtc.ICEServer
	if viper.IsSet("ice_servers") {
		data, _ := json.Marshal(viper.Get("ice_servers"))
		if err := json.Unmarshal(data, &iceServers); err != nil {
			log.Fatalf("Failed to parse ice_servers from config: %v", err)
		}
		duplexCfg.ICEServers = iceServers
	}
	if iceFlag := viper.GetString("ice_servers_flag"); iceFlag != "" {
		if err := json.Unmarshal([]byte(iceFlag), &iceServers); err != nil {
			log.Fatalf("Failed to parse ice-servers flag: %v", err)
		}
		duplexCfg.ICEServers = iceServers
	}

	// Verify loaded configuration
	if !standaloneMode && designation == "" {
		log.Fatal("A designation is required. Please provide it via -designation or in a config.json file. See --help for more information.")
	}
	if sessionHostnameProvided {
		if !sessionSecureProvided || !sessionPortProvided {
			log.Fatal("When session-hostname is provided, both session-secure and session-port must also be provided explicitly. See --help for more information.")
		}
	}

	// Initialize the bridge server
	instance := server.New(&serverCfg, &duplexCfg)

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
