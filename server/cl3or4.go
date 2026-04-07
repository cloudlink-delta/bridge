package server

import (
	"fmt"
	"log"
	"maps"
	"strings"

	"github.com/bwmarrin/snowflake"
	"github.com/goccy/go-json"
	"github.com/kaptinlin/jsonschema"
)

func NewCL3or4Packet(c *Client) Protocol {
	return &CL3or4Packet{origin: c, dialect: c.dialect}
}

func (p *CL3or4Packet) New() Protocol {
	return &CL3or4Packet{origin: p.origin, dialect: p.dialect}
}

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

	origin  *Client `json:"-"` // Internal field to track the sender client
	dialect uint    `json:"-"`
}

func (p *CL3or4Packet) BroadcastMsg(clients []*Client, data any) {
	if p.origin == nil {
		log.Println("Error sending CL3/4 BroadcastMsg: Origin client is nil in packet")
		return
	}

	// Prepare base origin object once
	originUserObj := p.origin.GetUserObject()

	for _, client := range clients {
		if client.Protocol != nil && client.Protocol.DeriveProtocol() != p.DeriveProtocol() {
			client.manager.Unicast(client, p)
			continue
		}

		// Create packet tailored for this client's dialect
		packet := &CL3or4Packet{
			Command: "gmsg",
			Val:     data,
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

func (p *CL3or4Packet) BroadcastVar(clients []*Client, name any, val any) {
	if p.origin == nil {
		log.Println("Error sending CL3/4 BroadcastVar: Origin client is nil in packet")
		return
	}

	// Prepare base origin object once
	originUserObj := p.origin.GetUserObject()

	for _, client := range clients {
		if client.Protocol != nil && client.Protocol.DeriveProtocol() != p.DeriveProtocol() {
			go client.manager.Unicast(client, p)
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

func (p *CL3or4Packet) UnicastMsg(client *Client, data any) {
	if client == nil {
		log.Println("Error: UnicastMsg target is nil")
		return
	}
	if p.origin == nil {
		log.Println("Error: UnicastMsg origin is nil")
		return
	}
	if client.Protocol != nil && client.Protocol.DeriveProtocol() != p.DeriveProtocol() {
		go client.manager.Unicast(client, p)
		return
	}

	packet := &CL3or4Packet{
		Command: "pmsg",
		Val:     data,
	}

	switch client.dialect {
	case Dialect_CL3_0_1_7, Dialect_CL4_0_1_8, Dialect_CL4_0_1_9:
		packet.Origin = p.origin.Name
	case Dialect_CL4_0_2_0:
		packet.Origin = p.origin.GetUserObject()
	}

	// Rooms generally not needed for unicast responses from server
	client.writer <- packet // Send struct
}

func (p *CL3or4Packet) UnicastVar(client *Client, name any, val any) {
	if client == nil {
		log.Println("Error: UnicastVar target is nil")
		return
	}
	if p.origin == nil {
		log.Println("Error: UnicastVar origin is nil")
		return
	}
	if client.Protocol != nil && client.Protocol.DeriveProtocol() != p.DeriveProtocol() {
		go client.manager.Unicast(client, p)
		return
	}

	packet := &CL3or4Packet{
		Command: "pvar",
		Name:    name,
		Val:     val,
	}

	switch client.dialect {
	case Dialect_CL3_0_1_7, Dialect_CL4_0_1_8, Dialect_CL4_0_1_9:
		packet.Origin = p.origin.Name
	case Dialect_CL4_0_2_0:
		packet.Origin = p.origin.GetUserObject()
	}

	// Rooms generally not needed for unicast responses from server
	client.writer <- packet // Send struct
}

func (p *CL3or4Packet) BroadcastBinder() chan any { return nil }

func (p *CL3or4Packet) UnicastBinder() chan any { return nil }

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
		return false
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

	// Attempt to auto-detect (or upgrade) the protocol dialect
	p.DeriveDialect(p.origin)
	return true
}

func (p *CL3or4Packet) Bytes() []byte {
	// Marshal the packet itself
	b, err := json.Marshal(p)
	if err != nil {
		log.Println("CL3/4 JSON Marshal Error (Bytes):", err)
		return nil
	}
	return b
}

func (p *CL3or4Packet) String() string {
	// Marshal the packet itself for an accurate representation
	b, err := json.Marshal(p)
	if err != nil {
		log.Println("CL3/4 JSON Marshal Error (String):", err)
		return fmt.Sprintf("{Error marshaling packet: %v}", err)
	}
	return string(b)
}

func (p *CL3or4Packet) DeriveProtocol() uint {
	return Protocol_CL3or4
}

func (p *CL3or4Packet) DeriveDialect(c *Client) uint {
	if p.Command == "handshake" {
		if valMap, ok := p.Val.(map[string]any); ok {
			_, langExists := valMap["language"]
			_, versExists := valMap["version"]
			if langExists && versExists {
				p.UpgradeDialect(c, Dialect_CL4_0_2_0)
			} else {
				p.UpgradeDialect(c, Dialect_CL4_0_1_9)
			}
		} else {
			p.UpgradeDialect(c, Dialect_CL4_0_1_9)
		}
	} else if p.Command == "link" || (p.Listener != nil && p.Listener != "") {
		p.UpgradeDialect(c, Dialect_CL4_0_1_8)
	} else if p.Command == "direct" && isTypeDeclaration(p.Val) {
		p.UpgradeDialect(c, Dialect_CL3_0_1_7)
	} else {
		p.UpgradeDialect(c, Dialect_CL3_0_1_5)
	}
	return c.dialect
}

func (p *CL3or4Packet) Handler(c *Client, m *Manager) {

	// Attempt to detect updates to the protocol if delayed
	p.DeriveDialect(p.origin)

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
			p.SendStatuscode(c, "I:100 | OK", 100, "Handshake already complete", nil, p.Listener)
			return
		}
		c.UpdateHandshake(true)
		c.JoinRoom(m.DefaultRoom)
		p.SendInitialState(c, m)
		p.SendStatuscode(c, "I:100 | OK", 100, nil, nil, p.Listener)

	case "gmsg":
		if p.Val == nil {
			p.SendStatuscode(c, "E:101 | Syntax", 101, "Message missing required val key", nil, p.Listener)
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
			p.SendStatuscode(c, "E:101 | Syntax", 101, "Message missing required val or name key", nil, p.Listener)
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
				p.SendStatuscode(c, "E:102 | Datatype", 102, "Variable name must be a string", nil, p.Listener)
			}
		}

	case "pvar":
		if !c.NameSet {
			p.SendStatuscode(c, "E:111 | ID required", 111, nil, nil, p.Listener)
			return
		}
		if p.Val == nil || p.Name == nil || p.ID == nil {
			p.SendStatuscode(c, "E:101 | Syntax", 101, "Message missing required val, name, or id key", nil, p.Listener)
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
					p.SendStatuscode(c, "E:102 | Datatype", 102, "Variable name must be a string", nil, p.Listener)
					// Don't set targetsFound = true if name is invalid for this target
				}
			}
		}
		if !targetsFound && len(targetIDs) > 0 { // Check if any targets were specified but none found/valid
			log.Printf("Target client(s) %v not found for private variable from %s", p.ID, c.ID)
			p.SendStatuscode(c, "E:110 | Not found", 110, "Target ID(s) not found", nil, p.Listener)
		}

	case "pmsg":
		if !c.NameSet {
			p.SendStatuscode(c, "E:111 | ID required", 111, nil, nil, p.Listener)
			return
		}
		if p.Val == nil || p.ID == nil {
			p.SendStatuscode(c, "E:101 | Syntax", 101, "Message missing required val or id key", nil, p.Listener)
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
			p.SendStatuscode(c, "E:110 | Not found", 110, "Target ID(s) not found", nil, p.Listener)
		}

	case "setid":
		if c.NameSet {
			p.SendStatuscode(c, "E:107 | ID already set", 107, nil, c.GetUserObject(), p.Listener)
			return
		}
		if err := validateUsername(p.Val); err != nil {
			p.SendStatuscode(c, "E:102 | Datatype", 102, err.Error(), nil, p.Listener)
			return
		}
		// Convert username to string for check
		usernameStr := fmt.Sprintf("%v", p.Val)
		if m.IsUsernameTaken(usernameStr, c.ID) {
			p.SendStatuscode(c, "E:108 | ID taken", 108, "Username is already taken", nil, p.Listener)
			return
		}

		if c.dialect < Dialect_CL3_0_1_7 && !c.Handshake {
			c.UpdateHandshake(true)
			c.JoinRoom(m.DefaultRoom)
			p.SendHandshakeCompat(c, m)
		}

		c.SetName(p.Val)

		for _, room := range c.Rooms {
			p.BroadcastUserlistEvent(room, c, "add", true)
			p.SendUserlist(c, room)
		}
		p.SendStatuscode(c, "I:100 | OK", 100, nil, c.GetUserObject(), p.Listener)

	case "link":
		if !c.NameSet {
			p.SendStatuscode(c, "E:111 | ID required", 111, nil, nil, p.Listener)
			return
		}
		roomsToLink, err := ToSlice(p.Val)
		if err != nil {
			p.SendStatuscode(c, "E:101 | Syntax", 101, "Val must be a single room name/ID or an array", nil, p.Listener)
			return
		}
		linkedAny := false
		for _, roomNameAny := range roomsToLink {
			if err := validateUsername(roomNameAny); err != nil { // Use validateUsername for type check
				p.SendStatuscode(c, "E:102 | Datatype", 102, fmt.Sprintf("Room name '%v' is not a valid type", roomNameAny), nil, p.Listener)
				continue
			}
			roomNameStr := fmt.Sprintf("%v", roomNameAny) // Convert to string for map key
			room := m.CreateRoom(roomNameStr)
			if _, alreadyJoined := c.Rooms[roomNameStr]; !alreadyJoined {
				c.JoinRoom(room)
				log.Printf("Client %s linked to room %v", c.ID, roomNameStr)
				p.SendRoomState(c, room)
				linkedAny = true
			}
		}
		if linkedAny || len(roomsToLink) > 0 {
			p.SendStatuscode(c, "I:100 | OK", 100, nil, nil, p.Listener)
		}

	case "unlink":
		if !c.NameSet {
			p.SendStatuscode(c, "E:111 | ID required", 111, nil, nil, p.Listener)
			return
		}
		roomsToUnlink := make(map[any]*Room) // Use string key
		if p.Val == nil || p.Val == "" {
			maps.Copy(roomsToUnlink, c.Rooms)
		} else {
			specifiedRooms, err := ToSlice(p.Val)
			if err != nil {
				p.SendStatuscode(c, "E:101 | Syntax", 101, "Val must be nil, empty string, a single room name/ID, or an array", nil, p.Listener)
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
			p.SendRoomState(c, defaultRoom) // Send state if they just rejoined default
		}

		if unlinkedAny {
			p.SendStatuscode(c, "I:100 | OK", 100, nil, nil, p.Listener)
		} else {
			p.SendStatuscode(c, "E:110 | Not found", 110, "Specified room(s) not found or already unlinked", nil, p.Listener)
		}

	case "direct": // Only reached if dialect > CL3_0_1_7 or de-nesting failed
		log.Printf("Received direct command (not CL3 nested): %v", p.Val)
		if p.Listener != nil && p.Listener != "" {
			p.SendStatuscode(c, "I:100 | OK", 100, nil, nil, p.Listener)
		}

	case "echo":
		p.origin = nil // Clear internal origin before echoing
		c.writer <- p

	default:
		log.Printf("Unknown or unhandled CL3/CL4 Command: %s from client %s", p.Command, c.ID)
		p.SendStatuscode(c, "E:109 | Invalid command", 109, nil, nil, p.Listener)
	}
}

func (p *CL3or4Packet) IsJSON() bool { return true }

func (p *CL3or4Packet) SpoofServerVersion() string {
	dialect := p.dialect
	if p.origin != nil {
		dialect = p.origin.dialect
	}

	switch dialect {
	default:
		return "0.1.5"
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
	}
}

func (p *CL3or4Packet) ToGeneric() *Generic {
	g := &Generic{
		Origin:  p.origin,
		Payload: p,
	}

	switch p.Command {
	case "gmsg", "gvar":
		g.Opcode = Generic_Broadcast
	case "pmsg", "pvar":
		g.Opcode = Generic_Unicast
	case "ulist":
		g.Opcode = Generic_Userlist_Event
	default:
		g.Opcode = Generic_Direct
	}

	return g
}

func (p *CL3or4Packet) FromGeneric(g *Generic) {
	p.origin = g.Origin
	if p.origin != nil {
		p.Origin = p.origin.GetUserObject()
	}

	var val, name any
	switch payload := g.Payload.(type) {
	case *CL3or4Packet:
		val = payload.Val
		name = payload.Name
		p.Rooms = payload.Rooms
	case *CL2Packet:
		val = payload.Data
		name = payload.Var
	case *ScratchPacket:
		val = payload.Value
		name = payload.Name
	}

	switch g.Opcode {
	case Generic_Broadcast:
		if name != nil {
			p.Command = "gvar"
			p.Name = name
		} else {
			p.Command = "gmsg"
		}
		p.Val = val
	case Generic_Unicast:
		if name != nil {
			p.Command = "pvar"
			p.Name = name
		} else {
			p.Command = "pmsg"
		}
		p.Val = val
	case Generic_Direct:
		p.Command = "direct"
		p.Val = val
	}
}

func (p *CL3or4Packet) UpgradeDialect(c *Client, newdialect uint) {
	if newdialect > c.dialect {

		var basestring string
		if c.dialect == Dialect_Undefined {
			basestring = fmt.Sprintf("%s 𝐢 Detected ", c.GiveName())
		} else {
			basestring = fmt.Sprintf("%s 𝐢 Upgraded to ", c.GiveName())
		}

		c.dialect = newdialect
		p.dialect = newdialect
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

func (p *CL3or4Packet) SendStatuscode(c *Client, code string, codeID int, details any, val any, listener any) {
	if c.dialect < Dialect_CL3_0_1_7 {
		return
	}
	switch c.dialect {
	case Dialect_CL3_0_1_7:
		c.writer <- &CL3or4Packet{
			Command:  "statuscode",
			Val:      code,
			Listener: listener,
		}

	default:
		c.writer <- &CL3or4Packet{
			Command:  "statuscode",
			Code:     code,
			CodeID:   codeID,
			Details:  details,
			Val:      val,
			Listener: listener,
		}
	}
}

func (p *CL3or4Packet) SendRoomState(c *Client, room *Room) {
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
	p.SendUserlist(c, room) // Handles its own locking
	for name, value := range gvars {
		c.writer <- &CL3or4Packet{
			Command: "gvar",
			Name:    name,
			Val:     value,
			Rooms:   room.Name,
		}
	}
}

func (p *CL3or4Packet) SendInitialState(c *Client, m *Manager) {
	p.SendClientObject(c) // Send client object first if supported
	p.SendServerVersion(c, m)
	p.SendMOTD(c, m)
	// Send initial state for all currently subscribed rooms
	c.roomlock.Lock() // Lock before iterating rooms map
	roomsCopy := make([]*Room, 0, len(c.Rooms))
	for _, room := range c.Rooms {
		roomsCopy = append(roomsCopy, room)
	}
	c.roomlock.Unlock() // Unlock after copying

	for _, room := range roomsCopy {
		p.SendRoomState(c, room)
	}
}

func (p *CL3or4Packet) SendHandshakeCompat(c *Client, m *Manager) {
	p.SendServerVersion(c, m)
	p.SendMOTD(c, m)
}

// SendUserlist sends the userlist in the correct format for the client's dialect.
func (p *CL3or4Packet) SendUserlist(c *Client, room *Room) {
	packet := &CL3or4Packet{Command: "ulist", Rooms: room.Name}
	if c.dialect < Dialect_CL4_0_1_8 {
		ulist := room.GenerateUserlistString()
		if ulist == "ul;" {
			packet.Val = ""
		} else if strings.HasSuffix(ulist, "ul;") {
			packet.Val = strings.TrimSuffix(ulist, "ul;")
		} else {
			packet.Val = ulist
		}
	} else if c.dialect >= Dialect_CL4_0_2_0 {
		packet.Mode = "set"
		packet.Val = room.GenerateUserObjectList()
	} else {
		slice := room.GenerateUserlistSlice()
		if strSlice, ok := slice.([]string); ok {
			if len(strSlice) == 1 && strSlice[0] == "ul" {
				packet.Val = []string{}
			} else if len(strSlice) > 0 && strSlice[len(strSlice)-1] == "ul" {
				packet.Val = strSlice[:len(strSlice)-1]
			} else {
				packet.Val = slice
			}
		} else if anySlice, ok := slice.([]any); ok {
			if len(anySlice) == 1 && anySlice[0] == "ul" {
				packet.Val = []any{}
			} else if len(anySlice) > 0 && anySlice[len(anySlice)-1] == "ul" {
				packet.Val = anySlice[:len(anySlice)-1]
			} else {
				packet.Val = slice
			}
		} else {
			packet.Val = slice
		}
	}
	c.writer <- packet
}

// SendServerVersion sends the server version in the correct format.
func (p *CL3or4Packet) SendServerVersion(c *Client, m *Manager) {
	spoofedVersion := p.SpoofServerVersion() // Assuming this method exists

	switch c.dialect {
	case Dialect_CL3_0_1_5, Dialect_CL3_0_1_7:
		// CL3 0.1.5-0.1.7 expects nesting
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
func (p *CL3or4Packet) SendMOTD(c *Client, m *Manager) {

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
func (p *CL3or4Packet) SendClientObject(c *Client) {
	if c.dialect >= Dialect_CL4_0_2_0 {
		c.writer <- &CL3or4Packet{
			Command: "client_obj",
			Val:     c.GetUserObject(),
		}
	}
}

func (p *CL3or4Packet) BroadcastUserlistEvent(room *Room, updatedClient *Client, event string, exclude bool) {
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
		// Skip those without names (can't be in ulist yet)
		if client.Name == nil {
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
			p.SendUserlist(client, room) // SendUserlist determines the correct format (string/slice)
		}
	}
}
