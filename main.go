package main

import (
	"log"

	cloudlink "github.com/cloudlink-delta/bridge/server"
	"github.com/goccy/go-json"
	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
)

func main() {
	app := fiber.New(fiber.Config{
		JSONEncoder: json.Marshal,
		JSONDecoder: json.Unmarshal,
	})
	manager := cloudlink.NewManager()

	app.Use("/*", func(c *fiber.Ctx) error {
		if websocket.IsWebSocketUpgrade(c) {
			c.Locals("allowed", true)
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})

	app.Get("/*", websocket.New(func(c *websocket.Conn) {
		client := manager.Create(c)
		defer manager.Destroy(client)
		client.Runner()
	}))

	log.Fatal(app.Listen("localhost:3000"))
}
