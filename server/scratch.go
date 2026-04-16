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
	schema, err := jsonschema.FromStruct[ScratchPacket]()
	if err != nil {
		panic(err)
	}

	return &Scratch_Handler{
		Schema: schema,
		Server: parent,
	}
}

func (s Scratch_Handler) On_Disconnect(c *BridgeClient, rooms RoomKeys) {
	if c.Username == nil || c.Username == "" {
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
		if client.Username != nil && client.Username != "" {
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
		client.Username = p.User
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
		if client.Username == nil || len(client.Rooms) == 0 {
			return
		}
		projectRoom := client.Rooms[0]

		if gv := s.GetRoomGlobalVars(projectRoom); gv != nil {
			gv.Store(p.Name, p.Value)
		}

		s.Broadcast(projectRoom, &ScratchPacket{
			Method: p.Method,
			Name:   p.Name,
			Value:  p.Value,
		})

	case "rename":
		if client.Username == nil || len(client.Rooms) == 0 {
			return
		}
		projectRoom := client.Rooms[0]

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
		if client.Username == nil || len(client.Rooms) == 0 {
			return
		}
		projectRoom := client.Rooms[0]

		if gv := s.GetRoomGlobalVars(projectRoom); gv != nil {
			gv.Delete(p.Name)
		}

		s.Broadcast(projectRoom, &ScratchPacket{
			Method: "delete",
			Name:   p.Name,
		})
	}
}
