package main

import (
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/cloudlink-delta/bridge/server"

	"github.com/cloudlink-delta/duplex"
	"github.com/goccy/go-json"
	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
)

func main() {
	// Define a globally unique designation that will be used to identify this bridge server.
	const DESIGNATION = "bridge@US-NKY-1"

	// Create instance and bridge manager
	instance := duplex.New(DESIGNATION)
	instance.IsBridge = true
	app := fiber.New(fiber.Config{
		JSONEncoder: json.Marshal,
		JSONDecoder: json.Unmarshal,
	})
	manager := server.New(instance, &server.Config{
		Enable_MOTD:        true,
		MOTD_Message:       "CloudLink Bridge Server - Use bridge@US-NKY-1 to connect on CloudLink Delta!",
		Serve_IP_Addresses: true,
		Maximum_Rooms:      100,
		Maximum_Clients:    1000,
		Force_Set:          true,
	})

	// Configure bridge websocket
	app.Use("/*", func(c *fiber.Ctx) error {
		if websocket.IsWebSocketUpgrade(c) {
			c.Locals("allowed", true)
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})

	app.Get("/*", websocket.New(func(c *websocket.Conn) {
		manager.Run_Client(c)
	}))

	// Init waitgroup
	var wg sync.WaitGroup
	wg.Add(2) // Add 2 waitgroup tasks

	// Launch fiber app
	go func() {
		defer wg.Done()
		log.Fatal(app.Listen("localhost:3000"))
	}()

	// Launch instance app
	go func() {
		defer wg.Done()
		instance.Run()
	}()

	// Graceful shutdown handler
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		instance.Close <- true
		<-instance.Done
		os.Exit(1)
	}()

	wg.Wait() // Wait for both apps to finish
}
