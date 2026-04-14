package server

import (
	"fmt"
	"slices"
)

func (CL4_or_CL3) isTypeDeclaration(val any) bool {
	if valMap, ok := val.(map[string]any); ok {
		if cmd, ok := valMap["cmd"].(string); ok && cmd == "type" {
			return true
		}
	}
	return false
}

// Returns a formatted user object.
func (s *Server) UserObject(c *BridgeClient) *CL4_UserObject {
	return &CL4_UserObject{ID: c.ID, UUID: c.UUID, Username: c.Username}
}

func (s *Server) Get_User_List(room RoomKey, filter ...*BridgeClient) []*CL4_UserObject {
	clients := s.Copy_Clients(room)
	fullList := make([]*CL4_UserObject, 0)

	for _, client := range clients {

		if len(filter) > 0 && slices.Contains(filter, client) {
			continue
		}

		if client.Username != nil {
			fullList = append(fullList, s.UserObject(client))
		}
	}

	return fullList
}

// Get_Clients resolves a CloudLink ID/Username (or an array of them) to a map of clients for Multicasting
func (s *Server) Get_Clients(room RoomKey, targetID any) Targets {
	targets := make(Targets)
	clients := s.Copy_Clients(room)

	// Convert targetID to a slice so we can uniformly process 1 or multiple targets
	var targetIDs []any
	if slice, ok := targetID.([]any); ok {
		targetIDs = slice
	} else {
		targetIDs = []any{targetID}
	}

	for _, tID := range targetIDs {
		tIDStr := fmt.Sprintf("%v", tID) // Stringify for safe comparison
		for _, client := range clients {
			if client.ID == tIDStr || fmt.Sprintf("%v", client.Username) == tIDStr || client.UUID == tIDStr {
				targets[client] = true
			}
		}
	}

	return targets
}

// Get_Target_Rooms converts the dynamic Rooms field into a slice of strings, defaulting to the client's current rooms.
func (s *Server) Get_Target_Rooms(client *BridgeClient, roomsContext any) RoomKeys {
	if roomsContext == nil || roomsContext == "" {
		// If the client's rooms list is empty, default to DEFAULT_ROOM defensively
		if len(client.Rooms) == 0 {
			return RoomKeys{DEFAULT_ROOM}
		}
		return client.Rooms
	}

	var targetRooms RoomKeys
	// JSON arrays unmarshal into []any
	if slice, ok := roomsContext.([]any); ok {
		for _, r := range slice {
			targetRooms = append(targetRooms, RoomKey(fmt.Sprintf("%v", r)))
		}
	} else {
		// Fallback for single room string/number
		targetRooms = append(targetRooms, RoomKey(fmt.Sprintf("%v", roomsContext)))
	}
	return targetRooms
}

// Is_Client_In_Room checks if the client is currently subscribed to a specific room
func (s *Server) Is_Client_In_Room(client *BridgeClient, room RoomKey) bool {
	clientsInRoom := s.Copy_Clients(room)
	for _, c := range clientsInRoom {
		if c.UUID == client.UUID {
			return true
		}
	}
	return false
}

// Sync_Room_State loops through a room's global variables and unicasts them to a client
func (s *Server) Sync_Room_State(client *BridgeClient, room RoomKey) {
	s.roomsMu.RLock()
	r, exists := s.RoomsMap[room]
	s.roomsMu.RUnlock()
	if exists {
		r.GlobalVars.Range(func(key, value any) bool {
			s.Unicast(client, &Common_Packet{
				Command: "gvar",
				Name:    key,
				Value:   value,
				Rooms:   room,
			})
			return true
		})
	}
}
