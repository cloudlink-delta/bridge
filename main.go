package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/cloudlink-delta/bridge/server"
)

func main() {
	// Define a globally unique designation that will be used to identify this bridge server.
	const DESIGNATION = "SOMEWHERE-EARTH"

	instance := server.New(DESIGNATION, &server.Config{
		Enable_MOTD:        true,
		MOTD_Message:       "Welcome to the CloudLink Bridge!",
		Serve_IP_Addresses: true,
		Maximum_Rooms:      100,
		Maximum_Clients:    1000,
		Force_Set:          true,
		Address:            "127.0.0.1:3000",
	}, nil)

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
