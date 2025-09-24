package cloudlink

import (
	"log"

	"github.com/goccy/go-json"

	"github.com/gofiber/contrib/websocket"
)

func (client *Client) CloseWithMessage(statuscode int, closeMessage string) {
	client.connection.WriteMessage(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(
			statuscode,
			closeMessage,
		),
	)
	client.connection.Close()
}

func (client *Client) MessageHandler(manager *Manager) {
	// websocket.Conn bindings https://pkg.go.dev/github.com/fasthttp/websocket?tab=doc#pkg-index
	var (
		_       int
		message []byte
		err     error
	)
	for {
		// Listen for new messages
		if _, message, err = client.connection.ReadMessage(); err != nil {
			log.Printf("Client %s (%s) read error: %s", client.id, client.uuid, err)
			break
		}

		switch client.protocol {

		// Attempt to detect protocol
		case Protocol_Detecting:

			// CL2 protcol
			var cl2packet = NewCL2PacketFormatter()
			if _, ok := cl2packet.Parse(string(message)); ok {
				log.Println("Detected CL2 protocol")

				// Update client attributes
				client.protocol = Protocol_CL2 // CL2

				// Add the client to the default room
				defaultroom := client.manager.CreateRoom("default")
				defaultroom.SubscribeClient(client)

				// Process first packet
				CL2HandleMessage(client, string(message))
				continue
			}

			// CL4 protocol
			var cl4packet Packet_UPL
			if err := json.Unmarshal([]byte(message), &cl4packet); err != nil {
				client.CloseWithMessage(websocket.CloseUnsupportedData, "JSON parsing error")
			} else if cl4packet.Cmd != "" {

				// Update client attributes
				client.protocol = Protocol_CL4

				// Detect dialect
				if cl4packet.Cmd == "handshake" {
					// Check for the new v0.2.0 handshake format
					if valMap, ok := cl4packet.Val.(map[string]any); ok {
						_, langExists := valMap["language"]
						_, versExists := valMap["version"]
						if langExists && versExists {
							log.Println("Detected CL4 protocol with v0.2.0 dialect")
							client.dialect = Dialect_CL4_0_2_0
						} else {
							log.Println("Detected CL4 protocol with v0.1.9.x dialect")
							client.dialect = Dialect_CL4_0_1_9
						}
					} else {
						// val is missing or not an object, indicating the older handshake
						log.Println("Detected CL4 protocol with v0.1.9.x dialect")
						client.dialect = Dialect_CL4_0_1_9
					}

				} else if cl4packet.Cmd == "direct" && isTypeDeclaration(cl4packet.Val) {
					log.Println("Detected CL3 protocol with v0.1.7 compatible dialect")
					client.dialect = Dialect_CL3_0_1_7

				} else if cl4packet.Cmd == "link" || cl4packet.Listener != "" {
					log.Println("Detected CL4 protocol with v0.1.8.x dialect")
					client.dialect = Dialect_CL4_0_1_8

				} else {
					log.Println("Dialect detection failed, assuming CL3 protocol with v0.1.5 (or older) dialect")
					client.dialect = Dialect_CL3_0_1_5
				}

				// Add the client to the default room
				defaultroom := client.manager.CreateRoom("default")
				defaultroom.SubscribeClient(client)

				// Process first packet
				CL4MethodHandler(client, &cl4packet)
				continue
			}

			// Scratch protocol
			var scratchpacket Packet_CloudVarScratch
			if err := json.Unmarshal([]byte(message), &scratchpacket); err != nil {
				client.CloseWithMessage(websocket.CloseUnsupportedData, "JSON parsing error")
			} else if scratchpacket.Method != "" {
				log.Println("Detected Scratch protocol")

				// Update client attributes
				client.protocol = Protocol_CloudVars

				// Process first packet
				ScratchMethodHandler(client, &scratchpacket)
				continue
			}

			client.CloseWithMessage(websocket.CloseProtocolError, "Couldn't identify protocol")

		case Protocol_CL4: // CL4
			var cl4packet Packet_UPL
			if err := json.Unmarshal([]byte(message), &cl4packet); err != nil {
				client.CloseWithMessage(websocket.CloseUnsupportedData, "JSON parsing error")
			}
			CL4MethodHandler(client, &cl4packet)

		case Protocol_CloudVars: // Scratch
			var scratchpacket Packet_CloudVarScratch
			if err := json.Unmarshal([]byte(message), &scratchpacket); err != nil {
				client.CloseWithMessage(websocket.CloseUnsupportedData, "JSON parsing error")
			}
			ScratchMethodHandler(client, &scratchpacket)

		case Protocol_CL2: // CL2
			CL2HandleMessage(client, string(message))
		}
	}
}

// SessionHandler is the root function that makes CloudLink work. As soon as a client request gets upgraded to the websocket protocol, this function should be called.
func SessionHandler(con *websocket.Conn, manager *Manager) {
	/*
		// con.Locals is added to the *websocket.Conn
		log.Println(con.Locals("allowed"))  // true
		log.Println(con.Params("id"))       // 123
		log.Println(con.Query("v"))         // 1.0
		log.Println(con.Cookies("session")) // ""
	*/

	// Register client
	client := NewClient(con, manager)
	manager.AddClient(client)

	// Log IP address of client (if enabled)
	if manager.Config.CheckIPAddresses {
		log.Printf("[%s] Client %s (%s) IP address: %s", manager.name, client.id, client.uuid, con.RemoteAddr().String())
	}

	// Remove client from manager once the session has ended
	defer manager.RemoveClient(client)

	// Begin handling messages throughout the lifespan of the connection
	client.MessageHandler(manager)
}

// isTypeDeclaration is a helper to check for the CL3 direct/type command
func isTypeDeclaration(val any) bool {
	if valMap, ok := val.(map[string]any); ok {
		if cmd, ok := valMap["cmd"].(string); ok && cmd == "type" {
			return true
		}
	}
	return false
}
