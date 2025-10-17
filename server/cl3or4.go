package cloudlink

import (
	"fmt"
	"log"
	"maps"

	"github.com/bwmarrin/snowflake"
	"github.com/google/uuid"
	"github.com/kaptinlin/jsonschema"

	"github.com/goccy/go-json"
)

// CL3or4Packet represents a parsed CL3 or CL4 packet
type CL3or4Packet struct {
	Command   string `json:"cmd" jsonschema:"required"`
	Name      any    `json:"name,omitempty"`
	Data      any    `json:"data,omitempty"` // For CL3 0.1.5 dialect
	Val       any    `json:"val,omitempty"`
	ID        any    `json:"id,omitempty"`    // Recipient(s) for pmsg/pvar from client
	Rooms     any    `json:"rooms,omitempty"` // Target room(s) for gmsg/gvar from client, or context for server->client
	Listener  any    `json:"listener,omitempty"`
	Code      string `json:"code,omitempty"`
	CodeID    int    `json:"code_id,omitempty"`
	Mode      string `json:"mode,omitempty"`   // For ulist updates
	Origin    any    `json:"origin,omitempty"` // For server->client responses, contains UserObject
	Details   any    `json:"details,omitempty"`
	Recipient any    `json:"recipient,omitempty"` // Legacy/alternative for ID

	origin *Client `json:"-"` // Internal field to track the sender client
}

// UserObject structure represents a client for JSON transport
type UserObject struct {
	ID       snowflake.ID `json:"id,omitempty"`
	Username any          `json:"username,omitempty"`
	UUID     uuid.UUID    `json:"uuid,omitempty"`
}

func (p *CL3or4Packet) DeriveProtocol() uint {
	return Protocol_CL3or4 // Use constant value
}

// DeriveDialect determines the specific CL3/CL4 version based on packet quirks.
// If it determines that a packet is for a newer dialect, it will upgrade the client's dialect status.
func (p *CL3or4Packet) DeriveDialect(c *Client) uint {
	if p.Command == "handshake" {
		if valMap, ok := p.Val.(map[string]any); ok {
			_, langExists := valMap["language"]
			_, versExists := valMap["version"]
			if langExists && versExists {
				c.UpgradeDialect(Dialect_CL4_0_2_0)
			} else {
				c.UpgradeDialect(Dialect_CL4_0_1_9)
			}
		} else {
			c.UpgradeDialect(Dialect_CL4_0_1_9)
		}
	} else if p.Command == "link" || (p.Listener != nil && p.Listener != "") {
		c.UpgradeDialect(Dialect_CL4_0_1_8)
	} else if p.Command == "direct" && isTypeDeclaration(p.Val) {
		c.UpgradeDialect(Dialect_CL3_0_1_7)
	} else {
		c.UpgradeDialect(Dialect_CL3_0_1_5)
	}
	return c.dialect
}

func (p *CL3or4Packet) IsJSON() bool {
	return true
}

// Reader validates and unmarshals incoming JSON data into the packet struct.
func (p *CL3or4Packet) Reader(data []byte) bool {
	if !json.Valid(data) {
		log.Println("CL3/4 Reader: Invalid JSON received")
		return false
	}

	// Schema validation (using jsonschema)
	schema := jsonschema.FromStruct[CL3or4Packet]() // Consider caching this
	result := schema.Validate(data)
	if !result.IsValid() {
		log.Printf("CL3/4 Packet failed schema validation: %v", result)
		// Optionally return false for strict validation
	}

	// Unmarshal into the struct
	if err := json.Unmarshal(data, &p); err != nil {
		log.Printf("CL3/4 JSON Unmarshal Error: %s", err)
		return false
	}

	// Basic check: Command must exist
	if p.Command == "" {
		log.Println("CL3/4 Reader: Packet missing required 'cmd' field")
		return false
	}

	return true
}

// String provides a JSON string representation of the packet for logging/debugging.
func (p *CL3or4Packet) String() string {
	// Marshal the packet itself for an accurate representation
	b, err := json.Marshal(p)
	if err != nil {
		log.Println("CL3/4 JSON Marshal Error (String):", err)
		return fmt.Sprintf("{Error marshaling packet: %v}", err)
	}
	return string(b)
}

// Bytes marshals the packet into JSON bytes for sending over the websocket.
func (p *CL3or4Packet) Bytes() []byte {
	// Marshal the packet itself
	b, err := json.Marshal(p)
	if err != nil {
		log.Println("CL3/4 JSON Marshal Error (Bytes):", err)
		return nil
	}
	return b
}

// Handler routes incoming CL3/CL4 commands.
func (p *CL3or4Packet) Handler(c *Client, m *Manager) {
	p.origin = c // Store the origin client for use in sending functions

	// De-nest CL3 'direct' commands if needed
	if p.Command == "direct" && c.dialect == Dialect_CL3_0_1_7 {
		if valMap, ok := p.Val.(map[string]any); ok {
			if cmd, ok := valMap["cmd"].(string); ok {
				log.Println("De-nesting CL3 'direct' command...")
				p.Command = cmd
				if val, exists := valMap["val"]; exists {
					p.Val = val
				} else {
					p.Val = nil
				}
			}
		}
	}

	switch p.Command {
	case "handshake", "type": // type is for 0.1.7 dialect (de-nested from direct)
		if c.Handshake {
			SendStatuscode(c, "I:100 | OK", 100, "Handshake already complete", nil, p.Listener)
			return
		}
		c.UpdateHandshake(true)
		c.JoinRoom(m.DefaultRoom)
		SendInitialState(c, m)
		SendStatuscode(c, "I:100 | OK", 100, nil, nil, p.Listener)

	case "gmsg":
		if p.Val == nil {
			SendStatuscode(c, "E:101 | Syntax", 101, "Message missing required val key", nil, p.Listener)
			return
		}
		roomsToBroadcast := c.getTargetRooms(p.Rooms)
		for _, room := range roomsToBroadcast {
			room.GmsgState = p.Val
			clientsToSend := room.ClientsAsSlice()
			p.BroadcastMsg(clientsToSend, p.Val)
		}

	case "gvar":
		if p.Val == nil || p.Name == nil {
			SendStatuscode(c, "E:101 | Syntax", 101, "Message missing required val or name key", nil, p.Listener)
			return
		}
		roomsToBroadcast := c.getTargetRooms(p.Rooms)
		for _, room := range roomsToBroadcast {
			room.SetGvar(p.Name, p.Val)
			clientsToSend := room.ClientsAsSlice()
			if nameStr, ok := p.Name.(string); ok {
				p.BroadcastVar(clientsToSend, nameStr, p.Val)
			} else {
				log.Printf("Error: gvar name is not a string: %v", p.Name)
				SendStatuscode(c, "E:102 | Datatype", 102, "Variable name must be a string", nil, p.Listener)
			}
		}

	case "pvar":
		if !c.NameSet {
			SendStatuscode(c, "E:111 | ID required", 111, nil, nil, p.Listener)
			return
		}
		if p.Val == nil || p.Name == nil || p.ID == nil {
			SendStatuscode(c, "E:101 | Syntax", 101, "Message missing required val, name, or id key", nil, p.Listener)
			return
		}
		targetsFound := false
		targetIDs, _ := ToSlice(p.ID)
		var targetClient *Client
		var found bool

		for _, targetID := range targetIDs {
			targetClient, found = m.FindClient(targetID) // Use Manager's FindClient
			if found {
				if nameStr, ok := p.Name.(string); ok {
					p.UnicastVar(targetClient, nameStr, p.Val)
					targetsFound = true
				} else {
					log.Printf("Error: pvar name is not a string: %v", p.Name)
					SendStatuscode(c, "E:102 | Datatype", 102, "Variable name must be a string", nil, p.Listener)
					// Don't set targetsFound = true if name is invalid for this target
				}
			}
		}
		if !targetsFound && len(targetIDs) > 0 { // Check if any targets were specified but none found/valid
			log.Printf("Target client(s) %v not found for private variable from %s", p.ID, c.ID)
			SendStatuscode(c, "E:110 | Not found", 110, "Target ID(s) not found", nil, p.Listener)
		}

	case "pmsg":
		if !c.NameSet {
			SendStatuscode(c, "E:111 | ID required", 111, nil, nil, p.Listener)
			return
		}
		if p.Val == nil || p.ID == nil {
			SendStatuscode(c, "E:101 | Syntax", 101, "Message missing required val or id key", nil, p.Listener)
			return
		}
		targetsFound := false
		targetIDs, _ := ToSlice(p.ID)
		var targetClient *Client
		var found bool

		for _, targetID := range targetIDs {
			targetClient, found = m.FindClient(targetID)
			if found {
				p.UnicastMsg(targetClient, p.Val)
				targetsFound = true
			}
		}
		if !targetsFound && len(targetIDs) > 0 {
			log.Printf("Target client(s) %v not found for private message from %s", p.ID, c.ID)
			SendStatuscode(c, "E:110 | Not found", 110, "Target ID(s) not found", nil, p.Listener)
		}

	case "setid":
		if c.NameSet {
			SendStatuscode(c, "E:107 | ID already set", 107, nil, c.GetUserObject(), p.Listener)
			return
		}
		if err := validateUsername(p.Val); err != nil {
			SendStatuscode(c, "E:102 | Datatype", 102, err.Error(), nil, p.Listener)
			return
		}
		// Convert username to string for check
		usernameStr := fmt.Sprintf("%v", p.Val)
		if m.IsUsernameTaken(usernameStr, c.ID) {
			SendStatuscode(c, "E:108 | ID taken", 108, "Username is already taken", nil, p.Listener)
			return
		}

		if c.dialect < Dialect_CL3_0_1_7 && !c.Handshake {
			c.UpdateHandshake(true)
			c.JoinRoom(m.DefaultRoom)
			SendHandshakeCompat(c, m)
		}

		c.SetName(p.Val)

		for _, room := range c.Rooms {
			BroadcastUserlistEvent(room, c, "add", true)
			SendUserlist(c, room)
		}
		SendStatuscode(c, "I:100 | OK", 100, nil, c.GetUserObject(), p.Listener)

	case "link":
		if !c.NameSet {
			SendStatuscode(c, "E:111 | ID required", 111, nil, nil, p.Listener)
			return
		}
		roomsToLink, err := ToSlice(p.Val)
		if err != nil {
			SendStatuscode(c, "E:101 | Syntax", 101, "Val must be a single room name/ID or an array", nil, p.Listener)
			return
		}
		linkedAny := false
		for _, roomNameAny := range roomsToLink {
			if err := validateUsername(roomNameAny); err != nil { // Use validateUsername for type check
				SendStatuscode(c, "E:102 | Datatype", 102, fmt.Sprintf("Room name '%v' is not a valid type", roomNameAny), nil, p.Listener)
				continue
			}
			roomNameStr := fmt.Sprintf("%v", roomNameAny) // Convert to string for map key
			room := m.CreateRoom(roomNameStr)
			if _, alreadyJoined := c.Rooms[roomNameStr]; !alreadyJoined {
				c.JoinRoom(room)
				log.Printf("Client %s linked to room %v", c.ID, roomNameStr)
				SendRoomState(c, room)
				linkedAny = true
			}
		}
		if linkedAny || len(roomsToLink) > 0 {
			SendStatuscode(c, "I:100 | OK", 100, nil, nil, p.Listener)
		}

	case "unlink":
		if !c.NameSet {
			SendStatuscode(c, "E:111 | ID required", 111, nil, nil, p.Listener)
			return
		}
		roomsToUnlink := make(map[any]*Room) // Use string key
		if p.Val == nil || p.Val == "" {
			maps.Copy(roomsToUnlink, c.Rooms)
		} else {
			specifiedRooms, err := ToSlice(p.Val)
			if err != nil {
				SendStatuscode(c, "E:101 | Syntax", 101, "Val must be nil, empty string, a single room name/ID, or an array", nil, p.Listener)
				return
			}
			for _, roomNameAny := range specifiedRooms {
				roomNameStr := fmt.Sprintf("%v", roomNameAny)
				if room, ok := c.Rooms[roomNameStr]; ok {
					roomsToUnlink[roomNameStr] = room
				}
			}
		}

		unlinkedAny := false
		for roomName, room := range roomsToUnlink {
			if roomName == "default" {
				continue
			}
			c.LeaveRoom(room) // LeaveRoom now handles userlist broadcast and potential destruction
			log.Printf("Client %s unlinked from room %v", c.ID, roomName)
			unlinkedAny = true
		}

		// Re-subscribe to default if needed (handled within LeaveRoom's logic via manager.RemoveClient implicitly)
		// Ensure default room exists and client is in it after mass unlink
		if _, inDefault := c.Rooms["default"]; !inDefault && len(roomsToUnlink) > 0 {
			defaultRoom := m.CreateRoom("default")
			c.JoinRoom(defaultRoom)
			SendRoomState(c, defaultRoom) // Send state if they just rejoined default
		}

		if unlinkedAny {
			SendStatuscode(c, "I:100 | OK", 100, nil, nil, p.Listener)
		} else {
			SendStatuscode(c, "E:110 | Not found", 110, "Specified room(s) not found or already unlinked", nil, p.Listener)
		}

	case "direct": // Only reached if dialect > CL3_0_1_7 or de-nesting failed
		log.Printf("Received direct command (not CL3 nested): %v", p.Val)
		if p.Listener != nil && p.Listener != "" {
			SendStatuscode(c, "I:100 | OK", 100, nil, nil, p.Listener)
		}

	case "echo":
		p.origin = nil // Clear internal origin before echoing
		c.writer <- p

	default:
		log.Printf("Unknown or unhandled CL3/CL4 Command: %s from client %s", p.Command, c.ID)
		SendStatuscode(c, "E:109 | Invalid command", 109, nil, nil, p.Listener)
	}
}

// --- Abstractor functions ---

// SendStatuscode sends a status code packet, formatting correctly.
func SendStatuscode(c *Client, code string, codeID int, details any, val any, listener any) {
	if c.dialect < Dialect_CL3_0_1_7 {
		return
	}
	c.writer <- &CL3or4Packet{
		Command:  "statuscode",
		Code:     code,
		CodeID:   codeID,
		Details:  details,
		Val:      val,
		Listener: listener,
	}
}

// SendInitialState sends the initial burst of info after handshake.
func SendInitialState(c *Client, m *Manager) {
	SendClientObject(c) // Send client object first if supported
	SendServerVersion(c, m)
	SendMOTD(c, m)
	// Send initial state for all currently subscribed rooms
	c.roomlock.Lock() // Lock before iterating rooms map
	roomsCopy := make([]*Room, 0, len(c.Rooms))
	for _, room := range c.Rooms {
		roomsCopy = append(roomsCopy, room)
	}
	c.roomlock.Unlock() // Unlock after copying

	for _, room := range roomsCopy {
		SendRoomState(c, room)
	}
}

// SendHandshakeCompat sends minimal info for very old CL3 clients after setid.
func SendHandshakeCompat(c *Client, m *Manager) {
	SendServerVersion(c, m)
	SendMOTD(c, m)
}

// SendRoomState sends GMSG, Userlist, and GVARs for a room to a client.
func SendRoomState(c *Client, room *Room) {
	// Use read locks for accessing room state
	room.lock.Lock()
	gmsg := room.GmsgState
	gvars := maps.Clone(room.GvarStates) // Clone map for safe iteration
	room.lock.Unlock()

	c.writer <- &CL3or4Packet{
		Command: "gmsg",
		Val:     gmsg,
		Rooms:   room.Name,
	}
	SendUserlist(c, room) // Handles its own locking
	for name, value := range gvars {
		c.writer <- &CL3or4Packet{
			Command: "gvar",
			Name:    name,
			Val:     value,
			Rooms:   room.Name,
		}
	}
}

// SendUserlist sends the userlist in the correct format for the client's dialect.
func SendUserlist(c *Client, room *Room) {
	packet := &CL3or4Packet{Command: "ulist", Rooms: room.Name}
	if c.dialect < Dialect_CL4_0_1_8 {
		packet.Val = room.GenerateUserlistString()
	} else if c.dialect >= Dialect_CL4_0_2_0 {
		packet.Mode = "set"
		packet.Val = room.GenerateUserObjectList()
	} else {
		packet.Val = room.GenerateUserlistSlice()
	}
	c.writer <- packet
}

// SendServerVersion sends the server version in the correct format.
func SendServerVersion(c *Client, m *Manager) {
	spoofedVersion := c.SpoofServerVersion() // Assuming this method exists

	switch c.dialect {
	case Dialect_CL3_0_1_5:
		// CL3 0.1.5 expects nesting
		c.writer <- &CL3or4Packet{
			Command: "direct",
			Data: map[string]any{
				"cmd":  "vers",         // Inner command key is 'cmd'
				"data": spoofedVersion, // Inner value key is 'val'
			},
		}
	case Dialect_CL3_0_1_7:
		// CL3 0.1.5 expects slightly different nesting
		c.writer <- &CL3or4Packet{
			Command: "direct",
			Val: map[string]any{
				"cmd": "vers",         // Inner command key is 'cmd'
				"val": spoofedVersion, // Inner value key is 'val'
			},
		}
	default: // CL4+ uses top-level command
		c.writer <- &CL3or4Packet{
			Command: "server_version",
			Val:     spoofedVersion,
		}
	}
}

// SendMOTD sends the MOTD if supported and enabled.
func SendMOTD(c *Client, m *Manager) {

	// Only send if MOTD is enabled
	if !m.Config.EnableMOTD {
		return
	}

	// 0.1.5 dialect DOES NOT support MOTD.
	if c.dialect == Dialect_CL3_0_1_5 {
		return
	}

	// Use default MOTD if none is set
	motd := m.Config.MOTDMessage
	if motd == "" {
		motd = "CloudLink Bridge Server v" + m.ServerVersion
	}

	switch c.dialect {
	case Dialect_CL3_0_1_7:
		// CL3 0.1.7 expects MOTD inside 'direct'
		c.writer <- &CL3or4Packet{
			Command: "direct",
			Val: map[string]any{
				"cmd": "motd", // Inner command key is 'cmd'
				"val": motd,   // Inner value key is 'val'
			},
		}
	default: // CL4+ uses top-level 'motd'
		c.writer <- &CL3or4Packet{
			Command: "motd",
			Val:     motd,
		}
	}
}

// SendClientObject sends the client's own user object if supported.
func SendClientObject(c *Client) {
	if c.dialect >= Dialect_CL4_0_2_0 {
		c.writer <- &CL3or4Packet{
			Command: "client_obj",
			Val:     c.GetUserObject(),
		}
	}
}

// BroadcastUserlistEvent sends differential updates or full lists based on dialect.
func BroadcastUserlistEvent(room *Room, updatedClient *Client, event string, exclude bool) {
	// Separate clients by dialect capability
	olderClients := make(map[snowflake.ID]*Client)  // Clients needing full list updates
	latestClients := make(map[snowflake.ID]*Client) // Clients supporting differential updates

	room.lock.Lock() // Lock room while iterating clients
	clientsCopy := maps.Clone(room.Clients)
	room.lock.Unlock()

	for id, client := range clientsCopy {
		// Exclude the originating client if requested
		if exclude && client.ID == updatedClient.ID {
			continue
		}
		// Skip non-CL3/4 clients or those without names (can't be in ulist yet)
		if client.Name == nil || client.protocol != Protocol_CL3or4 {
			continue
		}

		if client.dialect < Dialect_CL4_0_2_0 {
			olderClients[id] = client
		} else {
			latestClients[id] = client
		}
	}

	// Send modern, event-based packets to latest clients using Manager's Multicast
	if len(latestClients) > 0 {
		eventPacket := &CL3or4Packet{
			Command: "ulist",
			Val:     updatedClient.GetUserObject(),
			Mode:    event, // "add" or "remove"
			Rooms:   room.Name,
		}
		// Assuming updatedClient has manager access if needed, or pass manager
		if room.manager != nil {
			room.manager.Multicast(latestClients, eventPacket)
		} else {
			log.Println("Warning: Cannot multicast userlist event, manager context missing.")
		}
	}

	// Send the full, updated userlist (formatted correctly by SendUserlist) to older clients individually
	if len(olderClients) > 0 {
		// Iterate and call the abstractor function which handles different list formats
		for _, client := range olderClients {
			SendUserlist(client, room) // SendUserlist determines the correct format (string/slice)
		}
	}
}

// --- Interface Implementation Methods ---

// --- Interface Implementation Methods with Dialect Quirks ---

// BroadcastMsg sends a global message ('gmsg' type) to a list of clients, respecting dialect.
func (p *CL3or4Packet) BroadcastMsg(clients []*Client, val any) {
	if p.origin == nil {
		log.Println("Error sending CL3/4 BroadcastMsg: Origin client is nil in packet")
		return
	}

	// Prepare base origin object once
	originUserObj := p.origin.GetUserObject()

	for _, client := range clients {
		// Basic protocol check
		if client.protocol != Protocol_CL3or4 {
			continue
		}

		// Create packet tailored for this client's dialect
		packet := &CL3or4Packet{
			Command: "gmsg",
			Val:     val,
		}

		// Add Origin only if dialect supports it
		if client.dialect >= Dialect_CL4_0_2_0 {
			packet.Origin = originUserObj
		}

		// Add Rooms context only if dialect supports it
		// Assuming Room context comes from p.Rooms if available, otherwise omitted
		if client.dialect >= Dialect_CL4_0_1_8 && p.Rooms != nil {
			packet.Rooms = p.Rooms
		}

		client.writer <- packet // Send struct to writer channel
	}
}

// BroadcastVar sends a global variable update ('gvar' type) to a list of clients, respecting dialect.
func (p *CL3or4Packet) BroadcastVar(clients []*Client, name any, val any) {
	if p.origin == nil {
		log.Println("Error sending CL3/4 BroadcastVar: Origin client is nil in packet")
		return
	}

	// Prepare base origin object once
	originUserObj := p.origin.GetUserObject()

	for _, client := range clients {
		if client.protocol != Protocol_CL3or4 {
			continue
		}

		packet := &CL3or4Packet{
			Command: "gvar",
			Name:    name,
			Val:     val,
		}

		if client.dialect >= Dialect_CL4_0_2_0 {
			packet.Origin = originUserObj
		}
		if client.dialect >= Dialect_CL4_0_1_8 && p.Rooms != nil {
			packet.Rooms = p.Rooms
		}

		client.writer <- packet
	}
}

// UnicastMsg sends a private message ('pmsg' type) to a specific client, respecting dialect.
func (p *CL3or4Packet) UnicastMsg(target *Client, val any) {
	if target == nil {
		log.Println("Error: UnicastMsg target is nil")
		return
	}
	if p.origin == nil {
		log.Println("Error: UnicastMsg origin is nil")
		return
	}
	if target.protocol != Protocol_CL3or4 {
		return
	} // Basic protocol check

	packet := &CL3or4Packet{
		Command: "pmsg",
		Val:     val,
	}

	switch target.dialect {
	case Dialect_CL3_0_1_7, Dialect_CL4_0_1_8, Dialect_CL4_0_1_9:
		packet.Origin = p.origin.Name
	case Dialect_CL4_0_2_0:
		packet.Origin = p.origin.GetUserObject()
	}

	// Rooms generally not needed for unicast responses from server

	target.writer <- packet // Send struct
}

// UnicastVar sends a private variable update ('pvar' type) to a specific client, respecting dialect.
func (p *CL3or4Packet) UnicastVar(target *Client, name any, val any) {
	if target == nil {
		log.Println("Error: UnicastVar target is nil")
		return
	}
	if p.origin == nil {
		log.Println("Error: UnicastVar origin is nil")
		return
	}
	if target.protocol != Protocol_CL3or4 {
		return
	} // Basic protocol check

	packet := &CL3or4Packet{
		Command: "pvar",
		Name:    name,
		Val:     val,
	}

	switch target.dialect {
	case Dialect_CL3_0_1_7, Dialect_CL4_0_1_8, Dialect_CL4_0_1_9:
		packet.Origin = p.origin.Name
	case Dialect_CL4_0_2_0:
		packet.Origin = p.origin.GetUserObject()
	}

	// Rooms generally not needed for unicast responses from server

	target.writer <- packet // Send struct
}
