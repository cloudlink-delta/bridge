package cloudlink

import (
	"log"

	"github.com/bwmarrin/snowflake"
	"github.com/gofiber/contrib/websocket"
)

// MulticastMessage broadcasts a payload to multiple clients.
func MulticastMessage(clients map[snowflake.ID]*Client, message []byte) {
	for _, client := range clients {
		// Spawn goroutines to multicast the payload
		UnicastMessage(client, message)
	}
}

// UnicastMessageAny broadcasts a payload to a singular client.
func UnicastMessage(client *Client, message []byte) {
	// Lock state for the websocket connection to prevent accidental concurrent writes to websocket
	client.connectionMutex.Lock()
	// Attempt to send message to client
	if err := client.connection.WriteMessage(websocket.TextMessage, message); err != nil {
		log.Printf("Client %s (%s) send error: %s", client.id, client.uuid, err)
	}
	client.connectionMutex.Unlock()
}
