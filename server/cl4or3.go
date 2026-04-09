package server

import (
	"fmt"
	"log"

	"github.com/goccy/go-json"
	"github.com/kaptinlin/jsonschema"
)

// Creates a new instance of the protocol handler.
func New_CL4_or_CL3(parent *Server) Protocol {

	// Cache the schema
	schema, err := jsonschema.FromStruct[CL4_or_CL3_Packet]()
	if err != nil {
		panic(err)
	}

	// Return the new protocol instance
	return &CL4_or_CL3{
		Schema: schema,
		Server: parent,
	}
}

func (s CL4_or_CL3) On_Disconnect(c *Client, rooms RoomKeys) { // (And Scratch_Handler)
	if c.Username == nil || c.Username == "" {
		return
	}

	userObj := s.UserObject(c)

	for _, room := range rooms { // <--- Loop over `rooms` param
		s.Broadcast(room, &CL4_or_CL3_Packet{
			Command: "ulist",
			Mode:    "remove",
			Value:   userObj,
			Rooms:   room,
		}, c)
	}
}

func (s CL4_or_CL3) Reader(client *Client, data []byte) bool {
	// Check if the data is even remotely usable
	if !json.Valid(data) {
		log.Println("CL4/CL3 Reader: Invalid JSON received")
		return false
	}

	// Schema validation
	result := s.Schema.Validate(data)
	if !result.IsValid() {
		log.Printf("CL4/CL3 Packet failed schema validation: %v", result)
		return false
	}

	// Unmarshal into the struct
	var p *CL4_or_CL3_Packet
	if err := json.Unmarshal(data, &p); err != nil {
		log.Printf("CL4/CL3 JSON Unmarshal Error: %s", err)
		return false
	}

	// Basic check: Command must exist
	if p.Command == "" {
		log.Println("CL4/CL3 Reader: Packet missing required 'cmd' field")
		return false
	}

	// Attempt to auto-detect (or upgrade) the protocol dialect
	s.Derive_Dialect(p, client)

	// Interpret the protocol's commands
	go s.Handler(client, p)
	return true
}

// Main opcode handler for the bridge server
func (s CL4_or_CL3) Handler(client *Client, p *CL4_or_CL3_Packet) {
	log.Printf("%s 🢂  %v", client.GiveName(), p)

	switch p.Command {

	case "handshake":
		userObj := s.UserObject(client)
		s.Unicast(client, &CL4_or_CL3_Packet{Command: "server_version", Value: s.Spoof_Server_Version(client)})
		s.Unicast(client, &CL4_or_CL3_Packet{Command: "client_obj", Value: userObj})
		s.Unicast(client, &CL4_or_CL3_Packet{
			Command: "ulist",
			Mode:    "set",
			Value:   s.Get_User_List(DEFAULT_ROOM),
			Rooms:   DEFAULT_ROOM,
		})

		if client.Server.Config.Enable_MOTD {
			s.Unicast(client, &CL4_or_CL3_Packet{Command: "motd", Value: client.Server.Config.MOTD_Message})
		}
		if client.Server.Config.Serve_IP_Addresses {
			s.Unicast(client, &CL4_or_CL3_Packet{Command: "client_ip", Value: client.Conn.IP()})
		}
		s.Sync_Room_State(client, DEFAULT_ROOM)
		if p.Listener != nil {
			s.Send_Status_Code(client, StatusOK, p.Listener, nil, nil)
		}

	case "setid":
		if client.Username != nil && client.Username != "" {
			s.Send_Status_Code(client, StatusIDAlreadySet, p.Listener, nil, s.UserObject(client))
			return
		}

		if username, ok := p.Value.(string); ok {
			client.Username = username

			s.Send_Status_Code(client, StatusOK, p.Listener, nil, s.UserObject(client))

			s.Unicast(client, &CL4_or_CL3_Packet{
				Command: "ulist",
				Mode:    "set",
				Value:   s.Get_User_List(DEFAULT_ROOM),
				Rooms:   DEFAULT_ROOM,
			})

			s.Broadcast(DEFAULT_ROOM, &CL4_or_CL3_Packet{
				Command: "ulist",
				Mode:    "add",
				Value:   s.UserObject(client),
				Rooms:   DEFAULT_ROOM,
			}, client)
		}

		s.Sync_Room_State(client, DEFAULT_ROOM)

	case "gmsg", "gvar":
		targetRooms := s.Get_Target_Rooms(client, p.Rooms)

		for _, room := range targetRooms {
			if !s.Is_Client_In_Room(client, room) {
				s.Send_Status_Code(client, StatusRoomNotJoined, p.Listener, fmt.Sprintf("Attempted to access room %s while not joined.", room), nil)
				return
			}

			// Store the variable dynamically across all protocols
			if p.Command == "gvar" {
				s.roomsMu.RLock()
				r, exists := s.RoomsMap[room]
				s.roomsMu.RUnlock()
				if exists {
					r.GlobalVars.Store(p.Name, p.Value)
				}
			}

			s.Broadcast(room, &CL4_or_CL3_Packet{
				Command: p.Command,
				Value:   p.Value,
				Name:    p.Name,
				Origin:  s.UserObject(client),
				Rooms:   room,
			})
		}

		if p.Listener != nil {
			if client.dialect <= Dialect_CL4_0_1_9 {
				s.Unicast(client, &CL4_or_CL3_Packet{
					Command:  p.Command,
					Name:     p.Name,
					Value:    p.Value,
					Rooms:    p.Rooms,
					Listener: p.Listener,
				})
			} else {
				s.Send_Status_Code(client, StatusOK, p.Listener, nil, nil)
			}
		}

	case "pmsg", "pvar":
		if client.Username == nil || client.Username == "" {
			s.Send_Status_Code(client, StatusIDRequired, p.Listener, nil, nil)
			return
		}

		targetRooms := s.Get_Target_Rooms(client, p.Rooms)
		anyResultsFound := false

		for _, room := range targetRooms {
			if !s.Is_Client_In_Room(client, room) {
				s.Send_Status_Code(client, StatusRoomNotJoined, p.Listener, fmt.Sprintf("Attempted to access room %s while not joined.", room), nil)
				return
			}

			targets := s.Get_Clients(room, p.ID)
			if len(targets) > 0 {
				anyResultsFound = true
				s.Multicast(room, &CL4_or_CL3_Packet{
					Command: p.Command,
					Value:   p.Value,
					Name:    p.Name,
					Origin:  s.UserObject(client),
					Rooms:   room,
				}, targets)
			} else {
				s.Send_Status_Code(client, StatusIDNotFound, p.Listener, nil, nil)
				return
			}
		}

		if p.Listener != nil {
			if !anyResultsFound {
				s.Send_Status_Code(client, StatusIDNotFound, p.Listener, nil, nil)
			} else {
				s.Send_Status_Code(client, StatusOK, p.Listener, nil, nil)
			}
		}

	case "direct":
		targetRooms := s.Get_Target_Rooms(client, p.Rooms)
		anyResultsFound := false

		var originObj any
		if client.Username != nil && client.Username != "" {
			originObj = s.UserObject(client)
		} else {
			originObj = map[string]string{
				"id":   client.ID.String(),
				"uuid": client.UUID.String(),
			}
		}

		for _, room := range targetRooms {
			targets := s.Get_Clients(room, p.ID)
			if len(targets) > 0 {
				anyResultsFound = true
				s.Multicast(room, &CL4_or_CL3_Packet{
					Command: "direct",
					Value:   p.Value,
					Origin:  originObj,
				}, targets)
			}
		}

		if p.Listener != nil {
			if !anyResultsFound {
				s.Send_Status_Code(client, StatusIDNotFound, p.Listener, nil, nil)
			} else {
				s.Send_Status_Code(client, StatusOK, p.Listener, nil, nil)
			}
		}

	case "link":
		if client.Username == nil || client.Username == "" {
			s.Send_Status_Code(client, StatusIDRequired, p.Listener, nil, nil)
			return
		}

		roomsToLink := s.Get_Target_Rooms(client, p.Value)
		hasDefault := false

		if !s.CanAllocateNRooms(client, len(roomsToLink)) {
			s.Send_Status_Code(client, StatusRoomNotJoined, p.Listener, fmt.Sprintf("Cannot join %v room(s): The server is currently busy.", len(roomsToLink)), nil)
			return
		}

		for _, room := range roomsToLink {
			if room == DEFAULT_ROOM {
				hasDefault = true
			}
			s.Subscribe(client, room)

			// Broadcast addition to new room
			s.Broadcast(room, &CL4_or_CL3_Packet{
				Command: "ulist",
				Mode:    "add",
				Value:   s.UserObject(client),
				Rooms:   room,
			}, client)

			// Send the user the full list of the new room
			s.Unicast(client, &CL4_or_CL3_Packet{
				Command: "ulist",
				Mode:    "set",
				Value:   s.Get_User_List(room),
				Rooms:   room,
			})

			// Synchronize the room variable state
			s.Sync_Room_State(client, room)
		}

		// If default isn't explicitly requested, kick them from it
		if !hasDefault && s.Is_Client_In_Room(client, DEFAULT_ROOM) {
			s.Unsubscribe(client, DEFAULT_ROOM)
			s.Broadcast(DEFAULT_ROOM, &CL4_or_CL3_Packet{
				Command: "ulist",
				Mode:    "remove",
				Value:   s.UserObject(client),
				Rooms:   DEFAULT_ROOM,
			}, client)
		}

		if p.Listener != nil {
			s.Send_Status_Code(client, StatusOK, p.Listener, nil, nil)
		}

	case "unlink":
		if client.Username == nil || client.Username == "" {
			s.Send_Status_Code(client, StatusIDRequired, p.Listener, nil, nil)
			return
		}

		// Empty Value unlinks ALL rooms. Otherwise parse the list.
		var roomsToUnlink RoomKeys
		if p.Value == nil || p.Value == "" {
			// Unlink all current rooms the client is in
			roomsToUnlink = append(roomsToUnlink, client.Rooms...)
		} else {
			roomsToUnlink = s.Get_Target_Rooms(client, p.Value)
		}

		for _, room := range roomsToUnlink {
			log.Println("Unlinking from room", room)
			s.Unsubscribe(client, room)
			s.Broadcast(room, &CL4_or_CL3_Packet{
				Command: "ulist",
				Mode:    "remove",
				Value:   s.UserObject(client),
				Rooms:   room,
			}, client)
		}

		// If they are in 0 rooms, force them back into default
		if len(client.Rooms) == 0 {
			s.Subscribe(client, DEFAULT_ROOM)
			s.Broadcast(DEFAULT_ROOM, &CL4_or_CL3_Packet{
				Command: "ulist", Mode: "add", Value: s.UserObject(client), Rooms: DEFAULT_ROOM,
			}, client)
			s.Unicast(client, &CL4_or_CL3_Packet{
				Command: "ulist", Mode: "set", Value: s.Get_User_List(DEFAULT_ROOM), Rooms: DEFAULT_ROOM,
			})
		}

		if p.Listener != nil {
			s.Send_Status_Code(client, StatusOK, p.Listener, nil, nil)
		}
	}
}

// Builds and unicasts a status code packet to a client
func (s CL4_or_CL3) Send_Status_Code(client *Client, code StatusCode, listener any, details any, val any) {
	packet := &CL4_or_CL3_Packet{
		Command:  "statuscode",
		Code:     code.String(),
		CodeID:   code.Code,
		Listener: listener,
	}

	if details != nil {
		packet.Details = details
	}
	if val != nil {
		packet.Value = val
	}

	s.Unicast(client, packet)
}

// Generates a spoofed server version string to fool the client's compatibility checker
func (s CL4_or_CL3) Spoof_Server_Version(client *Client) string {
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

// Automatically determine the protocol version based on known first-packet behaviors
func (s CL4_or_CL3) Derive_Dialect(p *CL4_or_CL3_Packet, c *Client) {
	if p.Command == "handshake" {
		if valMap, ok := p.Value.(map[string]any); ok {
			_, langExists := valMap["language"]
			_, versExists := valMap["version"]
			if langExists && versExists {
				s.Upgrade_Dialect(c, Dialect_CL4_0_2_0)
			} else {
				s.Upgrade_Dialect(c, Dialect_CL4_0_1_9)
			}
		} else {
			s.Upgrade_Dialect(c, Dialect_CL4_0_1_9)
		}
	} else if p.Command == "link" || (p.Listener != nil && p.Listener != nil) {
		s.Upgrade_Dialect(c, Dialect_CL4_0_1_8)
	} else if p.Command == "direct" && s.isTypeDeclaration(p.Value) {
		s.Upgrade_Dialect(c, Dialect_CL3_0_1_7)
	} else {
		s.Upgrade_Dialect(c, Dialect_CL3_0_1_5)
	}
}

// Helper to automatically change the dialect version of the client based on known first-packet behaviors
func (p *CL4_or_CL3) Upgrade_Dialect(c *Client, newdialect uint) {
	if newdialect > c.dialect {

		var basestring string
		if c.dialect == Dialect_Undefined {
			basestring = fmt.Sprintf("%s 𝐢 Detected ", c.GiveName())
		} else {
			basestring = fmt.Sprintf("%s 𝐢 Upgraded to ", c.GiveName())
		}

		c.dialect = newdialect
		switch c.dialect {
		case Dialect_CL3_0_1_5:
			log.Println(basestring + "CL3 dialect v0.1.5")
		case Dialect_CL3_0_1_7:
			log.Println(basestring + "CL3 dialect v0.1.7")
		case Dialect_CL4_0_1_8:
			log.Println(basestring + "CL4 dialect v0.1.8")
		case Dialect_CL4_0_1_9:
			log.Println(basestring + "CL4 dialect v0.1.9")
		case Dialect_CL4_0_2_0:
			log.Println(basestring + "CL4 dialect v0.2.0")
		}
	}
}

// Sync_Room_State loops through a room's global variables and unicasts them to a client
func (s CL4_or_CL3) Sync_Room_State(client *Client, room RoomKey) {
	s.roomsMu.RLock()
	r, exists := s.RoomsMap[room]
	s.roomsMu.RUnlock()
	if exists {
		r.GlobalVars.Range(func(key, value any) bool {
			s.Unicast(client, &CL4_or_CL3_Packet{
				Command: "gvar",
				Name:    key,
				Value:   value,
				Rooms:   room,
			})
			return true
		})
	}
}
