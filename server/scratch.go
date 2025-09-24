package cloudlink

import (
	"github.com/gofiber/contrib/websocket"
)

// ScratchMethodHandler is a method that gets created when a Scratch-formatted message gets handled by MessageHandler.
func ScratchMethodHandler(client *Client, message *Packet_CloudVarScratch) {
	switch message.Method {
	case "handshake":

		// Validate datatype of project ID
		switch message.ProjectID.(type) {
		case string:
		case bool:
		case int64:
		case float64:
		default:
			client.CloseWithMessage(websocket.CloseUnsupportedData, "Invalid Project ID datatype")
			return
		}

		// Update client attributes
		client.username = message.Username

		// Creates room if it does not exist already
		room := client.manager.CreateRoom(message.ProjectID)
		room.SubscribeClient(client)

		// Update variable states
		for name, value := range room.gvarState {
			MulticastMessage(room.clients, Packet_CloudVarScratch{
				Method: "set",
				Value:  value,
				Name:   name,
			}.ToBytes())
		}

	case "set":
		for _, room := range client.rooms { // Should only ever have 1 entry
			// Update room gvar state
			room.gvarState[message.Name] = message.Value

			// Broadcast the new state
			MulticastMessage(room.clients, Packet_CloudVarScratch{
				Method: "set",
				Value:  room.gvarState[message.Name],
				Name:   message.Name,
			}.ToBytes())
		}

	case "create":
		for _, room := range client.rooms { // Should only ever have 1 entry

			// Update room gvar state
			room.gvarState[message.Name] = message.Value

			// Broadcast the new state
			MulticastMessage(room.clients, Packet_CloudVarScratch{
				Method: "create",
				Value:  room.gvarState[message.Name],
				Name:   message.Name,
			}.ToBytes())
		}

	case "rename":
		for _, room := range client.rooms { // Should only ever have 1 entry

			// Retrive old value
			oldvalue := room.gvarState[message.Name]

			// Destroy old value and make a new value
			delete(room.gvarState, message.Name)
			room.gvarState[message.NewName] = oldvalue

			// Broadcast the new state
			MulticastMessage(room.clients, Packet_CloudVarScratch{
				Method:  "rename",
				NewName: message.NewName,
				Name:    message.Name,
			}.ToBytes())
		}

	case "delete":
		for _, room := range client.rooms { // Should only ever have 1 entry

			// Destroy value
			delete(room.gvarState, message.Name)

			// Broadcast the new state
			MulticastMessage(room.clients, Packet_CloudVarScratch{
				Method: "delete",
				Name:   message.Name,
			}.ToBytes())
		}

	default:
		break
	}
}
