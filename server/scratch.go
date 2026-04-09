package server

import (
	"log"

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

func (s Scratch_Handler) On_Disconnect(c *Client, rooms RoomKeys) {
	if c.Username == nil || c.Username == "" {
		return
	}

	userObj := s.UserObject(c)

	for _, room := range rooms {
		s.Broadcast(room, &CL4_or_CL3_Packet{
			Command: "ulist",
			Mode:    "remove",
			Value:   userObj,
			Rooms:   room,
		}, c)
	}
}

func (s Scratch_Handler) Reader(client *Client, data []byte) bool {
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

func (s Scratch_Handler) Handler(client *Client, p *ScratchPacket) {
	log.Printf("%s 🢂  %s", client.GiveName(), p)

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

		// Sync Shared Variables!
		s.roomsMu.RLock()
		r, exists := s.RoomsMap[projectRoom]
		s.roomsMu.RUnlock()

		if exists {
			r.GlobalVars.Range(func(key, value any) bool {
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

		s.roomsMu.RLock()
		r, exists := s.RoomsMap[projectRoom]
		s.roomsMu.RUnlock()
		if exists {
			r.GlobalVars.Store(p.Name, p.Value)
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

		s.roomsMu.RLock()
		r, exists := s.RoomsMap[projectRoom]
		s.roomsMu.RUnlock()
		if exists {
			if oldVal, ok := r.GlobalVars.Load(p.Name); ok {
				r.GlobalVars.Store(p.NewName, oldVal)
				r.GlobalVars.Delete(p.Name)
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

		s.roomsMu.RLock()
		r, exists := s.RoomsMap[projectRoom]
		s.roomsMu.RUnlock()
		if exists {
			r.GlobalVars.Delete(p.Name)
		}

		s.Broadcast(projectRoom, &ScratchPacket{
			Method: "delete",
			Name:   p.Name,
		})
	}
}
