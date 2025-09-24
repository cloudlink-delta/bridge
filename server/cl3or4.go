package cloudlink

import (
	"log"
)

type UserObject struct {
	Id       string `json:"id,omitempty"`
	Username any    `json:"username,omitempty"`
	Uuid     string `json:"uuid,omitempty"`
}

func (client *Client) SpoofServerVersion() string {
	switch client.dialect {
	case Dialect_CL3_0_1_5:
		return "0.1.5"
	case Dialect_CL3_0_1_7:
		return "0.1.7"
	case Dialect_CL4_0_1_8:
		return "0.1.8"
	case Dialect_CL4_0_1_9:
		return "0.1.9"
	case Dialect_CL4_0_2_0:
		return "0.2.0"
	default:
		return "0.1.5"
	}
}

func (room *Room) BroadcastUserlistEvent(event string, client *Client, exclude bool) {
	// Create a dummy manager for selecting clients
	dummy := DummyManager(room.name)

	// Separate compatible clients
	for _, roomclient := range room.clients {
		tmpclient := roomclient.TempCopy()

		// Exclude handler
		excludeclientid := client.TempCopy().id
		if exclude && (tmpclient.id == excludeclientid) {
			continue
		}

		// Require a set username and a compatible protocol
		if (tmpclient.username == nil) || (tmpclient.protocol != Protocol_CL4) {
			continue
		}

		// Add client if passed
		dummy.AddClient(tmpclient)
	}

	// Broadcast state
	MulticastMessage(dummy.clients, Packet_UPL{
		Cmd:   "ulist",
		Val:   client.GenerateUserObject(),
		Mode:  event,
		Rooms: room.name,
	}.ToBytes())
}

func (room *Room) BroadcastGmsg(value any) {
	// Update room gmsg state
	room.gmsgStateMutex.Lock()
	room.gmsgState = value
	room.gmsgStateMutex.Unlock()

	// Broadcast the new state
	room.gmsgStateMutex.RLock()
	MulticastMessage(room.clients, Packet_UPL{
		Cmd:   "gmsg",
		Val:   room.gmsgState,
		Rooms: room.name,
	}.ToBytes())
	room.gmsgStateMutex.RUnlock()
}

func (room *Room) BroadcastGvar(name any, value any) {
	// Update room gmsg state
	room.gvarStateMutex.Lock()
	room.gvarState[name] = value
	room.gvarStateMutex.Unlock()

	// Broadcast the new state
	room.gvarStateMutex.RLock()
	MulticastMessage(room.clients, Packet_UPL{
		Cmd:   "gvar",
		Name:  name,
		Val:   room.gvarState[name],
		Rooms: room.name,
	}.ToBytes())
	room.gvarStateMutex.RUnlock()
}

func (client *Client) RequireIDBeingSet(message *Packet_UPL) bool {
	if !(client.TempCopy().nameset) {
		UnicastMessage(client, Packet_UPL{
			Cmd:      "statuscode",
			Code:     "E:111 | ID required",
			CodeID:   111,
			Listener: message.Listener,
		}.ToBytes())
		return true
	}
	return false
}

func (client *Client) HandleIDSet(message *Packet_UPL) bool {
	if client.TempCopy().nameset {
		UnicastMessage(client, Packet_UPL{
			Cmd:      "statuscode",
			Code:     "E:107 | ID already set",
			CodeID:   107,
			Val:      client.GenerateUserObject(),
			Listener: message.Listener,
		}.ToBytes())
		return true
	}
	return false
}

// CL4MethodHandler is a method that s created when a CL-formatted message gets handled by MessageHandler.

	// Check if we can upgrade the dialect detection from 0.1.9 to 0.2.0
	client.RLock()
	canUpgrade := (client.dialect == Dialect_CL4_0_1_9)
	client.RUnlock()

	if canUpgrade {
		// The signature of v0.2.0 is the use of native types instead of strings for complex values
		if _, isString := message.Val.(string); !isString {
			client.Lock()
			if client.dialect == Dialect_CL4_0_1_9 { // Double-check to avoid race conditions
				client.dialect = Dialect_CL4_0_2_0
				log.Printf("Client %s dialect upgraded to CL4 (v0.2.0)", client.id)
			}
			client.Unlock()
		}
	}

func CL4MethodHandler(client *Client, message *Packet_UPL) {
	switch message.Cmd {
	case "handshake":

		// Read attribute
		handshakeDone := (client.TempCopy().handshake)

		// Don't re-broadcast this data if the handshake command was already used
		if !handshakeDone {

			// Update attribute
			client.Lock()
			client.handshake = true
			client.Unlock()

			// Send the client's IP address
			if client.manager.Config.CheckIPAddresses {
				UnicastMessage(client, Packet_UPL{
					Cmd: "client_ip",
					Val: client.connection.Conn.RemoteAddr().String(),
				}.ToBytes())
			}

			// Send the server version info
			log.Printf("Client %s (%s) server version has been spoofed to %s", client.id, client.uuid, client.SpoofServerVersion())
			UnicastMessage(client, Packet_UPL{
				Cmd: "server_version",
				Val: client.SpoofServerVersion(),
			}.ToBytes())

			// Send MOTD
			if client.manager.Config.EnableMOTD {
				UnicastMessage(client, Packet_UPL{
					Cmd: "motd",
					Val: client.manager.Config.MOTDMessage + " (Running on v" + ServerVersion + ")",
				}.ToBytes())
			}

			// Send Client's object
			UnicastMessage(client, Packet_UPL{
				Cmd: "client_obj",
				Val: client.GenerateUserObject(),
			}.ToBytes())

			// Send gmsg, ulist, and gvar states
			rooms := client.TempCopy().rooms
			for _, room := range rooms {
				UnicastMessage(client, Packet_UPL{
					Cmd:   "gmsg",
					Val:   room.gmsgState,
					Rooms: room.name,
				}.ToBytes())
				UnicastMessage(client, Packet_UPL{
					Cmd:   "ulist",
					Mode:  "set",
					Val:   room.GenerateUserList(),
					Rooms: room.name,
				}.ToBytes())
				room.gvarStateMutex.RLock()
				for name, value := range room.gvarState {
					UnicastMessage(client, Packet_UPL{
						Cmd:   "gvar",
						Name:  name,
						Val:   value,
						Rooms: room.name,
					}.ToBytes())
				}
				room.gvarStateMutex.RUnlock()
			}
		}

		// Send status code
		UnicastMessage(client, Packet_UPL{
			Cmd:    "statuscode",
			Code:   "I:100 | OK",
			CodeID: 100,
		}.ToBytes())

	case "gmsg":
		// Check if required Val argument is provided
		switch message.Val.(type) {
		case nil:
			UnicastMessage(client, Packet_UPL{
				Cmd:     "statuscode",
				Code:    "E:101 | Syntax",
				CodeID:  101,
				Details: "Message missing required val key",
			}.ToBytes())
			return
		}

		// Handle multiple types for room
		switch message.Rooms.(type) {

		// Value not specified in message
		case nil:
			// Use all subscribed rooms
			rooms := client.TempCopy().rooms
			for _, room := range rooms {
				room.BroadcastGmsg(message.Val)
			}

		// Multiple rooms
		case []any:
			for _, room := range message.Rooms.([]any) {
				rooms := client.TempCopy().rooms

				// Check if room is valid and is subscribed
				if _, ok := rooms[room]; ok {
					rooms[room].BroadcastGmsg(message.Val)
				}
			}

		// Single room
		case any:
			// Check if room is valid and is subscribed
			rooms := client.TempCopy().rooms
			if _, ok := rooms[message.Rooms]; ok {
				rooms[message.Rooms].BroadcastGmsg(message.Val)
			}
		}

	case "pmsg":
		// Require username to be set before usage
		if client.RequireIDBeingSet(message) {
			return
		}

		rooms := TempCopyRooms(client.rooms)
		log.Printf("Searching for ID %s", message.ID)
		for _, room := range rooms {
			switch message.ID.(type) {
			case []any:
				for _, multiquery := range message.ID.([]any) {
					log.Printf("Room: %s - Multi query entry: %s, Result: %s", room.name, multiquery, room.FindClient(multiquery))
				}

			default:
				log.Printf("Room: %s - Result: %s", room.name, room.FindClient(message.ID))
			}
		}

	case "setid":
		// Val datatype validation
		switch message.Val.(type) {
		case string:
		case int64:
		case float64:
		case bool:
		default:
			// Send status code
			UnicastMessage(client, Packet_UPL{
				Cmd:      "statuscode",
				Code:     "E:102 | Datatype",
				CodeID:   102,
				Details:  "Username value (val) must be a string, boolean, float, or int",
				Listener: message.Listener,
			}.ToBytes())
			return
		}

		// Prevent changing usernames
		if client.HandleIDSet(message) {
			return
		}

		// Update client attributes
		client.Lock()
		client.username = message.Val
		client.nameset = true
		client.Unlock()

		// Use default room
		rooms := client.TempCopy().rooms
		for _, room := range rooms {
			room.BroadcastUserlistEvent("add", client, true)
			UnicastMessage(client, Packet_UPL{
				Cmd:   "ulist",
				Mode:  "set",
				Val:   room.GenerateUserList(),
				Rooms: room.name,
			}.ToBytes())
		}

		// Send status code
		UnicastMessage(client, Packet_UPL{
			Cmd:      "statuscode",
			Code:     "I:100 | OK",
			CodeID:   100,
			Val:      client.GenerateUserObject(),
			Listener: message.Listener,
		}.ToBytes())

	case "gvar":
		// Handle multiple types for room
		switch message.Rooms.(type) {

		// Value not specified in message
		case nil:
			// Use all subscribed rooms
			rooms := client.TempCopy().rooms
			for _, room := range rooms {
				room.BroadcastGvar(message.Name, message.Val)
			}

		// Multiple rooms
		case []any:
			// Use specified rooms
			for _, room := range message.Rooms.([]any) {
				rooms := client.TempCopy().rooms

				// Check if room is valid and is subscribed
				if _, ok := rooms[room]; ok {
					rooms[room].BroadcastGvar(message.Name, message.Val)
				}
			}

		// Single room
		case any:
			// Check if room is valid and is subscribed
			rooms := client.TempCopy().rooms
			if _, ok := rooms[message.Rooms]; ok {
				rooms[message.Rooms].BroadcastGvar(message.Name, message.Val)
			}
		}

	case "pvar":
		// Require username to be set before usage
		if !client.RequireIDBeingSet(message) {
			return
		}

	case "link":
		// Require username to be set before usage
		if !client.RequireIDBeingSet(message) {
			return
		}

		log.Printf("Linking client %s to %v...", client.id, message.Val)

		// Detect if single or multiple rooms
		switch message.Val.(type) {

		case nil:
			UnicastMessage(client, Packet_UPL{
				Cmd:     "statuscode",
				Code:    "E:101 | Syntax",
				CodeID:  101,
				Details: "Message missing required val key",
			}.ToBytes())
			return

		// Multiple rooms
		case []any:
			// Validate datatypes of array
			for _, elem := range message.Val.([]any) {
				switch elem.(type) {
				case string:
				case int64:
				case float64:
				case bool:
				default:
					// Send status code
					UnicastMessage(client, Packet_UPL{
						Cmd:      "statuscode",
						Code:     "E:102 | Datatype",
						CodeID:   102,
						Details:  "Multiple rooms value (val) must be an array of strings, bools, floats, or ints.",
						Listener: message.Listener,
					}.ToBytes())
					return
				}
			}
			// Subscribe to all rooms
			for _, name := range message.Val.([]any) {

				// Create room if it doesn't exist
				room := client.manager.CreateRoom(name)

				// Add the client to the room
				room.SubscribeClient(client)

				log.Println("Successfully linked client", client.id, "to", name)
			}

		// Single room
		case any:
			// Validate datatype
			switch message.Val.(type) {
			case string:
			case int64:
			case float64:
			case bool:
			default:
				// Send status code
				UnicastMessage(client, Packet_UPL{
					Cmd:      "statuscode",
					Code:     "E:102 | Datatype",
					CodeID:   102,
					Details:  "Single room value (val) must be a string, boolean, float, int.",
					Listener: message.Listener,
				}.ToBytes())
				return
			}

			// Subscribe to single room
			// Create room if it doesn't exist
			room := client.manager.CreateRoom(message.Val)

			// Add the client to the room
			room.SubscribeClient(client)
			log.Println("Successfully linked client", client.id, "to", message.Val)
		}

		// Send status code
		UnicastMessage(client, Packet_UPL{
			Cmd:    "statuscode",
			Code:   "I:100 | OK",
			CodeID: 100,
		}.ToBytes())

	case "unlink":
		// Require username to be set before usage
		if !client.RequireIDBeingSet(message) {
			return
		}
		// Detect if single or multiple rooms
		switch message.Val.(type) {

		case nil:
			// Unsubscribe all rooms and rejoin default
			rooms := client.TempCopy().rooms
			for _, room := range rooms {
				room.UnsubscribeClient(client)
				// Destroy room if empty, but don't destroy default room
				if len(room.clients) == 0 && (room.name != "default") {
					client.manager.DeleteRoom(room.name)
				}
			}

			// Get default room
			defaultroom := client.manager.CreateRoom("default")

			// Add the client to the room
			defaultroom.SubscribeClient(client)

		// Multiple rooms
		case []any:
			// Validate datatypes of array
			for _, elem := range message.Val.([]any) {
				switch elem.(type) {
				case string:
				case bool:
				case int64:
				case float64:
				default:
					// Send status code
					UnicastMessage(client, Packet_UPL{
						Cmd:      "statuscode",
						Code:     "E:102 | Datatype",
						CodeID:   102,
						Details:  "Multiple rooms value (val) must be an array of strings",
						Listener: message.Listener,
					}.ToBytes())
					return
				}
			}

			// Get currently subscribed rooms
			rooms := client.TempCopy().rooms

			// Validate room and verify that it was joined
			for _, _room := range message.Val.([]any) {
				if _, ok := rooms[_room]; ok {
					room := rooms[_room]
					room.UnsubscribeClient(client)
					// Destroy room if empty, but don't destroy default room
					if len(room.clients) == 0 && (room.name != "default") {
						client.manager.DeleteRoom(room.name)
					}
				}
			}

		// Single room
		case any:
			// Validate datatype
			switch message.Val.(type) {
			case string:
			case bool:
			case int64:
			case float64:
			default:
				// Send status code
				UnicastMessage(client, Packet_UPL{
					Cmd:      "statuscode",
					Code:     "E:102 | Datatype",
					CodeID:   102,
					Details:  "Single room value (val) must be a string",
					Listener: message.Listener,
				}.ToBytes())
				return
			}

			// Get currently subscribed rooms
			rooms := client.TempCopy().rooms

			// Validate if room is joined and remove client
			if _, ok := rooms[message.Val]; ok {
				room := rooms[message.Val]
				room.UnsubscribeClient(client)
				// Destroy room if empty, but don't destroy default room
				if len(room.clients) == 0 && (room.name != "default") {
					client.manager.DeleteRoom(room.name)
				}
			}
		}

		// Send status code
		UnicastMessage(client, Packet_UPL{
			Cmd:    "statuscode",
			Code:   "I:100 | OK",
			CodeID: 100,
		}.ToBytes())

	case "direct":
		// Require username to be set before usage
		if !client.RequireIDBeingSet(message) {
			return
		}

	case "echo":
		UnicastMessage(client, message.ToBytes())

	default:
		// Handle unknown commands
		UnicastMessage(client, Packet_UPL{
			Cmd:    "statuscode",
			Code:   "E:109 | Invalid command",
			CodeID: 109,
			// Val:      client.GenerateUserObject(),
			Listener: message.Listener,
		}.ToBytes())
	}
}
