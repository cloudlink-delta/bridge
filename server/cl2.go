package cloudlink

import (
	"log"
	"regexp"
	"strings"

	"github.com/goccy/go-json" // Using go-json for potential performance benefits
)

// Define a struct to hold regex patterns with their names for ordered matching.
type cl2Parser struct {
	name string
	re   *regexp.Regexp
}

// Define the parsers in a slice to enforce matching order (most specific first).
var orderedCL2Parsers []cl2Parser

func init() {
	orderedCL2Parsers = []cl2Parser{
		// Most specific patterns first (more parameters)
		{ // 5 fields after header: Handles <%l_p> Mode 1 and 2
			name: "linked_p_vars",
			re:   regexp.MustCompile(`^<%(l_p)>\s*\n(?P<Mode>[1-2])\s+(?P<Sender>[^\n]*)\s+(?P<Recipient>[^\n]*)\s+(?P<VarName>[^\n]*)\s+(?s)(?P<Data>.*)$`),
		},
		{ // 4 fields after header: Handles <%l_g> Mode 1 and 2
			name: "linked_g_vars",
			re:   regexp.MustCompile(`^<%(l_g)>\s*\n(?P<Mode>[1-2])\s+(?P<Sender>[^\n]*)\s+(?P<VarName>[^\n]*)\s+(?s)(?P<Data>.*)$`),
		},
		{ // 4 fields after header: Handles <%l_p> Mode 0
			name: "linked_p_data",
			re:   regexp.MustCompile(`^<%(l_p)>\s*\n(?P<Mode>0)\s+(?P<Sender>[^\n]*)\s+(?P<Recipient>[^\n]*)\s+(?s)(?P<Data>.*)$`),
		},
		{ // 3 fields after header: Handles <%ps>
			name: "private_stream",
			re:   regexp.MustCompile(`^<%ps>\s*\n(?P<Sender>[^\n]*)\s+(?P<Recipient>[^\n]*)\s+(?s)(?P<Data>.*)$`),
		},
		// NOTE: Assuming <%l_g> Mode 0 only has Sender and Data based on typical usage, making it 2 fields after header.
		// If it *can* have a Recipient field (even if ignored), you'd need a 3-field regex for it here.

		{ // 2 fields after header: Handles <%gs>
			name: "global_stream",
			re:   regexp.MustCompile(`^<%gs>\s*\n(?P<Sender>[^\n]*)\s+(?s)(?P<Data>.*)$`),
		},
		{ // 2 fields after header: Handles <%l_g> Mode 0
			name: "linked_g_data",
			re:   regexp.MustCompile(`^<%(l_g)>\s*\n(?P<Mode>0)\s+(?P<Sender>[^\n]*)\s+(?s)(?P<Data>.*)$`),
		},
		{ // 1 field after header: Handles <%sn>
			name: "set_username",
			re:   regexp.MustCompile(`^<%sn>\s*\n(?P<Sender>[^\n]*)$`),
		},
		{ // 1 field after header: Handles <%ds>
			name: "disconnect",
			re:   regexp.MustCompile(`^<%ds>\s*\n(?P<Sender>[^\n]*)$`),
		},
		{ // 0-1 field after header: Handles <%rf>, <%sh>, <%rt>
			name: "simple_cmd",
			re:   regexp.MustCompile(`^<%(rf|sh|rt)>\s*\n?(?P<Sender>[^\n]*)$`), // Keep \n? for optional sender
		},
	}
}

// CL2Packet represents a parsed CL2 command.
type CL2Packet struct {
	Command   string  `json:"cmd,omitempty"`
	Mode      string  `json:"mode,omitempty"`
	Sender    string  `json:"sender,omitempty"` // Parsed from packet
	Recipient string  `json:"recipient,omitempty"`
	Var       any     `json:"var,omitempty"`  // For variable commands
	Type      string  `json:"type,omitempty"` // For JSON response building
	Data      any     `json:"data,omitempty"` // Parsed data or data for JSON response
	ID        string  `json:"id,omitempty"`   // For private JSON responses
	origin    *Client `json:"-"`              // For private JSON responses
}

// CL2Packet_TxReply represents the outer structure for JSON responses.
type CL2Packet_TxReply struct {
	Type string `json:"type,omitempty"` // e.g., "gs", "ps", "sf", "direct"
	Data any    `json:"data,omitempty"` // Can be string or nested CL2Packet_TxData
	ID   string `json:"id,omitempty"`   // Recipient ID for "ps" type
}

// CL2Packet_TxData represents the nested 'data' object for special feature responses.
type CL2Packet_TxData struct {
	Type string `json:"type,omitempty"` // e.g., "gs", "ps", "vm", "vers"
	Mode string `json:"mode,omitempty"` // "g" or "p" for "vm" type
	Var  any    `json:"var,omitempty"`  // Variable name for "vm" type
	Data any    `json:"data"`           // Actual payload
}

func (p *CL2Packet) Bytes() []byte {
	// Build the appropriate JSON response based on the Type field
	var reply any
	if p.Type == "sf" || p.Type == "direct" { // Wrap special feature/direct responses
		reply = CL2Packet_TxReply{
			Type: p.Type,
			Data: p.Data, // Assumes Data is already a *CL2Packet_TxData struct
			ID:   p.ID,
		}
	} else { // Standard gs, ps, ul, ru responses
		reply = CL2Packet_TxReply{
			Type: p.Type,
			Data: p.Data, // Assumes Data is a string payload
			ID:   p.ID,
		}
	}

	b, err := json.Marshal(reply)
	if err != nil {
		log.Println("CL2 JSON Marshal Error:", err)
		return nil
	}
	return b
}

func (p *CL2Packet) String() string {
	b, err := json.Marshal(p)
	if err != nil {
		log.Println("CL2 JSON Marshal Error:", err)
		return ""
	}
	return string(b)
}

func (p *CL2Packet) DeriveProtocol() uint {
	return Protocol_CL2
}

func (p *CL2Packet) DeriveDialect(c *Client) uint {
	// CL2 dialect is determined by handshake status
	if c.Handshake {
		return Dialect_CL2_Late // Supports special features
	}
	return Dialect_CL2_Early // Basic CL2
}

func (p *CL2Packet) IsJSON() bool {
	return false // CL2 client packets are not JSON
}

// Takes a raw byte array and returns true if it is a semistructured CL2 packet.
// Also populates the provided packet struct with the extracted parameters.
func (h *CL2Packet) Reader(raw []byte) bool {
	message := string(raw)

	// Don't bother to parse the packet if the first two chars isn't "<%"
	if !strings.HasPrefix(message, "<%") {
		return false
	}

	for _, parser := range orderedCL2Parsers {
		if parser.re.MatchString(message) {
			matches := parser.re.FindStringSubmatch(message)
			names := parser.re.SubexpNames()
			parserName := parser.name

			// Clear the struct pointed to by h right before populating
			*h = CL2Packet{}

			captured := make(map[string]string)

			// Iterate through ALL capture groups (matches[1:])
			for i := 1; i < len(matches); i++ {
				name := names[i]
				match := matches[i]

				if name != "" { // It's a NAMED capture group
					captured[name] = match
					switch name {
					case "Sender":
						h.Sender = match
					case "Recipient":
						h.Recipient = match
					case "Data":
						// Assign data directly here
						h.Data = match
					case "Mode":
						h.Mode = match
					case "VarName":
						h.Var = match
					}
				} else { // It's an UNNAMED capture group
					// For specific parsers, the first unnamed group (i=1) is the command.
					if i == 1 && (strings.HasPrefix(parserName, "linked_") || parserName == "simple_cmd") {
						h.Command = match
						captured["Command (unnamed)"] = match
					}
				}
			}

			// Fallback Command Extraction (if not set by unnamed group)
			if h.Command == "" {
				if !(strings.HasPrefix(parserName, "linked_") || parserName == "simple_cmd") {
					h.Command = strings.Split(parserName, "_")[0]
				} else {
					// This case should ideally not be reached if regex and logic are correct
					log.Printf("Error: Command wasn't captured or derived for %s", parserName)
					// return false // Or handle error appropriately
				}
			}

			// Store raw Data string first
			rawDatString := ""
			if dataVal, ok := captured["Data"]; ok {
				rawDatString = dataVal
				h.Data = rawDatString // Assign raw string first
			}

			// Attempt to unmarshal Data if it was captured and looks like JSON
			var jsonData any
			if rawDatString != "" {
				if err := json.Unmarshal([]byte(rawDatString), &jsonData); err == nil {
					h.Data = jsonData // Overwrite Data field *only if* unmarshal succeeds
				}
				// If unmarshal fails, h.Data retains the raw string value - this is safer.
			}

			// Populate the packet struct
			return true // Successfully parsed
		}
	}
	log.Printf("Failed to parse CL2 packet: %s", message)
	return false
}

// Handler routes incoming CL2 commands.
func (p *CL2Packet) Handler(c *Client, m *Manager) {

	// Store the "sender" client
	p.origin = c

	switch p.Command {
	case "sh": // Special Handshake
		if c.Handshake {
			return // Already handshaked
		}
		c.UpdateHandshake(true)   // Mark client as supporting special features
		c.JoinRoom(m.DefaultRoom) // Add to default room if not already
		p.SendHandshake(c, m)     // Send back server version
		log.Printf("Client %s completed CL2 handshake", c.ID)

	case "rf": // Refresh User List
		log.Printf("Client %s requested user list refresh", c.ID)
		// Send the full user list string to this client
		p.UnicastUserlist(c, m.DefaultRoom.GenerateUserlistString())
		// Optionally, could broadcast <%ru%> to force all clients to resend <%sn%>

	case "set": // Set Username (comes from set_username regex -> "set")
		if c.NameSet {
			log.Printf("Client %s attempted to set username again", c.ID)
			return // Ignore if username already set
		}
		// Basic validation (e.g., length, reserved names) could go here
		c.SetName(p.Sender)
		log.Printf("Client %s set username to %s", c.ID, p.Sender)
		// Broadcast the updated user list to everyone in the default room
		m.DefaultRoom.BroadcastUserlist(m.DefaultRoom.GenerateUserlistString())

	case "global": // Global Stream Message (comes from global_stream regex -> "global")
		log.Printf("Client %s sent global message", c.ID)
		m.DefaultRoom.GmsgState = p.Data.(string) // Update room state
		// Broadcast the message to all clients in the default room
		p.BroadcastMsg(m.DefaultRoom.ClientsAsSlice(), p.Data)

	case "private": // Private Stream Message (comes from private_stream regex -> "private")
		log.Printf("Client %s sent private message to %s", c.ID, p.Recipient)
		if targetClient, found := m.DefaultRoom.FindClientByUsername(p.Recipient); found {
			p.UnicastMsg(targetClient, p.Data) // Send with "sender"
		} else {
			log.Printf("Target client %s not found for private message", p.Recipient)
			// Optionally send an error back to the sender
		}
		// Handle CL2 API calls embedded in <%ps>
		// This requires more complex parsing of p.Data if it's JSON

	case "l_g": // Linked Global Data/Var
		if !c.Handshake {
			return
		} // Ignore if no handshake
		switch p.Mode {
		case "0":
			log.Printf("Client %s sent linked global data", c.ID)
			// Logic to handle linked data (needs room/link state management)
			// TODO

		case "1", "2":
			log.Printf("Client %s set global var '%s'", c.ID, p.Var)
			// TODO
		}

	case "l_p": // Linked Private Data/Var
		if !c.Handshake {
			return
		} // Ignore if no handshake
		switch p.Mode {
		case "0": // Linked Private Data
			log.Printf("Client %s sent linked private data to %s", c.ID, p.Recipient)
			// Logic to handle linked data (needs room/link state management)
			// TODO

		case "1", "2": // Private Var
			log.Printf("Client %s set private var '%s' for %s", c.ID, p.Var, p.Recipient)
			// TODO
		}

	case "disconnect": // Disconnect command
		log.Printf("Client %s sent disconnect command", c.ID)
		// The main read loop usually handles the actual disconnect on error.
		// This handler is effectively a no-op.

	default:
		log.Printf("Unknown or unhandled CL2 Command: %s from client %s", p.Command, c.ID)
	}
}

// SendHandshake responds to a CL2 <%sh%> command.
func (p *CL2Packet) SendHandshake(c *Client, m *Manager) {
	resp := &CL2Packet{
		Type: "direct",
		Data: &CL2Packet_TxData{
			Type: "vers",
			Data: m.ServerVersion, // Use the server's version string
		},
	}
	c.writer <- resp.Bytes()
}

// BroadcastMsg sends a global message ('gs' type) to a list of clients.
func (p *CL2Packet) BroadcastMsg(clients []*Client, val any) {
	// Origin is not typically needed for CL2 global broadcasts from the server
	for _, client := range clients {
		if client.protocol != Protocol_CL2 {
			continue
		}

		var respData any
		respType := "gs"

		if client.Handshake { // Wrap for handshaked clients
			respType = "sf"
			respData = &CL2Packet_TxData{
				Type: "gs",
				Data: val, // Use the provided value directly
			}
		} else { // Standard response
			respData = val
		}

		resp := &CL2Packet{
			Type: respType,
			Data: respData,
		}
		client.writer <- resp.Bytes()
	}
}

// BroadcastVar sends a global variable update ('vm', mode 'g') to handshaked clients.
func (p *CL2Packet) BroadcastVar(clients []*Client, name any, val any) {
	// Origin is not typically needed for CL2 global broadcasts from the server
	for _, client := range clients {
		if client.protocol != Protocol_CL2 || !client.Handshake {
			continue
		}

		resp := &CL2Packet{
			Type: "sf",
			Data: &CL2Packet_TxData{
				Type: "vm",
				Mode: "g",
				Var:  name,
				Data: val, // Use the provided value directly
			},
		}
		client.writer <- resp.Bytes()
	}
}

// UnicastMsg sends a private message ('ps' type) to a specific client.
// Origin is derived from the packet's origin field.
func (p *CL2Packet) UnicastMsg(c *Client, val any) {
	if c.protocol != Protocol_CL2 {
		return
	}

	// Check if origin client exists on the packet
	if p.origin == nil {
		log.Println("Error sending CL2 UnicastMsg: Origin client is nil")
		return
	}

	var respData any
	respType := "ps"

	if c.Handshake { // Wrap for handshaked clients
		respType = "sf"
		respData = &CL2Packet_TxData{
			Type: "ps",
			Data: val, // Use the provided value directly
		}
	} else { // Standard response
		respData = val
	}

	resp := &CL2Packet{
		Type: respType,
		Data: respData,
		ID:   c.ID.String(), // Address to the recipient
		// Sender: p.origin.ID.String(), // CL2 server->client usually doesn't explicitly state sender
	}
	c.writer <- resp.Bytes()
}

// UnicastVar sends a private variable update ('vm', mode 'p') to a specific handshaked client.
// Origin is derived from the packet's origin field.
func (p *CL2Packet) UnicastVar(c *Client, name any, val any) {
	if c.protocol != Protocol_CL2 || !c.Handshake {
		return
	}

	// Check if origin client exists on the packet
	if p.origin == nil {
		log.Println("Error sending CL2 UnicastVar: Origin client is nil")
		return
	}

	resp := &CL2Packet{
		Type: "sf",
		ID:   c.ID.String(), // Address to the recipient
		Data: &CL2Packet_TxData{
			Type: "vm",
			Mode: "p",
			Var:  name,
			Data: val, // Use the provided value directly
		},
		// Sender: p.origin.ID.String(), // Optionally add origin if client needs it
	}
	c.writer <- resp.Bytes()
}

// UnicastUserlist sends the userlist ('ul' type) to a specific client.
func (p *CL2Packet) UnicastUserlist(c *Client, userlistString string) {
	if c.protocol != Protocol_CL2 {
		return
	}
	resp := &CL2Packet{
		Type: "ul",
		Data: userlistString,
	}
	c.writer <- resp.Bytes()
}

// BroadcastUserlist sends the userlist ('ul' type) to all CL2 clients in a room.
func (room *Room) BroadcastUserlist(userlistString string) {
	resp := &CL2Packet{
		Type: "ul",
		Data: userlistString,
	}
	payloadBytes := resp.Bytes()
	if payloadBytes == nil {
		return
	}

	for _, client := range room.Clients {
		if client.protocol == Protocol_CL2 {
			client.writer <- payloadBytes
		}
	}
}
