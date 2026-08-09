package server

import (
	"github.com/goccy/go-json"
	"github.com/kaptinlin/jsonschema"
)

type Scratch_Handler struct {
	Schema *jsonschema.Schema
	*Server
}

func New_Scratch(parent *Server) Protocol {
	return &Scratch_Handler{
		Schema: GetScratchPacketSchema(),
		Server: parent,
	}
}

func (s Scratch_Handler) On_Disconnect(c *BridgeClient, rooms RoomKeys) {
	username := c.GetUsername()
	if username == nil || username == "" {
		return
	}

	userObj := s.UserObject(c)

	for _, room := range rooms {
		s.Broadcast(room, &Common_Packet{
			Command: "ulist",
			Mode:    "remove",
			Value:   userObj,
			Rooms:   room,
		}, c)
	}
}

func (s Scratch_Handler) Reader(client *BridgeClient, data []byte) bool {
	if !json.Valid(data) {
		return false
	}
	result := s.Schema.Validate(data)
	if !result.IsValid() {
		return false
	}
	var p *ScratchPacket
	if err := json.Unmarshal(data, &p); err != nil {
		return false
	}
	if p.Method == "" {
		return false
	}

	go s.Handler(client, p)
	return true
}

func (s Scratch_Handler) Handler(client *BridgeClient, p *ScratchPacket) {
	if client == nil || client.Conn == nil {
		return
	}

	s.Logger.Debug().Any("packet", p).Any("client", client).Msg("Received Scratch packet")

	if client.Conn != nil {
		s.classicclientsmu.RLock()
		active := s.ClassicClients[client]
		s.classicclientsmu.RUnlock()
		if !active {
			return
		}
	}

	switch p.Method {
	case "handshake":

		// Don't allow repeated handshakes on the same session
		usernameVal := client.GetUsername()
		if usernameVal != nil && usernameVal != "" {
			s.Respond_With_Code(client.Conn, Generic_Error)
			client.Conn.Close()
			return
		}

		// Require a username
		if p.User == "" {
			s.Respond_With_Code(client.Conn, Username_Error)
			client.Conn.Close()
			return
		}

		// Set values for setup
		client.SetUsername(p.User)
		projectRoom := RoomKey(p.ProjectID)

		// Abort if the server is "busy"
		if !s.DoesRoomExist(projectRoom) && !s.CanAllocateNRooms(client, 1) {
			s.Respond_With_Code(client.Conn, Overloaded_Status)
			client.Conn.Close()
			return
		}

		// The Scratch protocol cannot use differing room contexts
		s.Unsubscribe(client, DEFAULT_ROOM)
		s.Subscribe(client, projectRoom)

		// Emit join event for other protocols
		s.Broadcast(projectRoom, &Common_Packet{
			Command: "ulist",
			Mode:    "add",
			Value:   s.UserObject(client),
			Rooms:   projectRoom,
		}, client)

		// Sync Shared Variables!
		if gv := s.GetRoomGlobalVars(projectRoom); gv != nil {
			gv.Range(func(key, value any) bool {
				s.Unicast(client, &ScratchPacket{
					Method: "set",
					Name:   key,
					Value:  value,
				})
				return true
			})
		}

	case "set", "create":
		rooms := client.GetRooms()
		if client.GetUsername() == nil || len(rooms) == 0 {
			return
		}
		projectRoom := rooms[0]

		s.SetRoomGlobalVar(client, projectRoom, p.Name, p.Value)

		s.Broadcast(projectRoom, &ScratchPacket{
			Method: p.Method,
			Name:   p.Name,
			Value:  p.Value,
		})

	case "rename":
		rooms := client.GetRooms()
		if client.GetUsername() == nil || len(rooms) == 0 {
			return
		}
		projectRoom := rooms[0]

		if gv := s.GetRoomGlobalVars(projectRoom); gv != nil {
			if oldVal, ok := gv.Load(p.Name); ok {
				gv.Store(p.NewName, oldVal)
				gv.Delete(p.Name)
			}
		}

		s.Broadcast(projectRoom, &ScratchPacket{
			Method:  "rename",
			Name:    p.Name,
			NewName: p.NewName,
		})

	case "delete":
		rooms := client.GetRooms()
		if client.GetUsername() == nil || len(rooms) == 0 {
			return
		}
		projectRoom := rooms[0]

		if gv := s.GetRoomGlobalVars(projectRoom); gv != nil {
			gv.Delete(p.Name)
		}

		s.Broadcast(projectRoom, &ScratchPacket{
			Method: "delete",
			Name:   p.Name,
		})
	}
}
