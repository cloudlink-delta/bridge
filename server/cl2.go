package server

import (
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/goccy/go-json"
)

func NewCL2Packet(c *Client) Protocol {
	return &CL2Packet{origin: c}
}

func (p *CL2Packet) New() Protocol {
	return &CL2Packet{origin: p.origin}
}

type CL2Packet struct {
	Command   string `json:"cmd,omitempty"`
	Mode      string `json:"mode,omitempty"`   // Can be string. "vm" type has "g" or "p"
	Sender    string `json:"sender,omitempty"` // Parsed from packet
	Recipient string `json:"recipient,omitempty"`
	Var       any    `json:"var,omitempty"`  // Variable name for "vm" type
	Type      string `json:"type,omitempty"` // For JSON response building, e.g. "gs", "ps", "sf", "direct", "vm", "vers"
	Data      any    `json:"data,omitempty"` // Parsed data or data for JSON response
	ID        any    `json:"id,omitempty"`   // Recipient ID for "ps" type

	origin  *Client `json:"-"` // Internal field to track the sender client
	dialect uint    `json:"-"`
}

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

func (p *CL2Packet) BroadcastMsg(clients []*Client, data any) {
	for _, client := range clients {
		if client.Protocol != nil && client.Protocol.DeriveProtocol() != p.DeriveProtocol() {
			client.manager.Unicast(client, p)
			continue
		}

		resp := &CL2Packet{
			Type: "gs",
			Data: data,
		}
		client.writer <- resp
	}
}

func (p *CL2Packet) BroadcastVar(clients []*Client, name any, val any) {
	for _, client := range clients {
		if client.Protocol != nil && client.Protocol.DeriveProtocol() != p.DeriveProtocol() {
			client.manager.Unicast(client, p)
			continue
		}
		if !client.Handshake {
			continue
		}

		resp := &CL2Packet{
			Type: "sf",
			Data: &CL2Packet{
				Type: "vm",
				Mode: "g",
				Var:  name,
				Data: val,
			},
		}
		client.writer <- resp
	}
}

func (p *CL2Packet) UnicastMsg(client *Client, data any) {
	if client.Protocol != nil && client.Protocol.DeriveProtocol() != p.DeriveProtocol() {
		client.manager.Unicast(client, p)
		return
	}

	resp := &CL2Packet{
		Type: "ps",
		Data: data,
		ID:   client.ID.String(),
	}
	client.writer <- resp
}

func (p *CL2Packet) UnicastVar(client *Client, name any, val any) {
	if client.Protocol != nil && client.Protocol.DeriveProtocol() != p.DeriveProtocol() {
		client.manager.Unicast(client, p)
		return
	}
	if !client.Handshake {
		return
	}

	resp := &CL2Packet{
		Type: "sf",
		ID:   client.ID.String(),
		Data: &CL2Packet{
			Type: "vm",
			Mode: "p",
			Var:  name,
			Data: val,
		},
	}
	client.writer <- resp
}

func (p *CL2Packet) BroadcastBinder() chan any { return nil }

func (p *CL2Packet) UnicastBinder() chan any { return nil }

func (p *CL2Packet) Reader(data []byte) bool {
	message := string(data)

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
			captured := make(map[string]string)

			// Iterate through ALL capture groups (matches[1:])
			for i := 1; i < len(matches); i++ {
				name := names[i]
				match := matches[i]

				if name != "" { // It's a NAMED capture group
					captured[name] = match
					switch name {
					case "Sender":
						p.Sender = match
					case "Recipient":
						p.Recipient = match
					case "Data":
						// Assign data directly here
						p.Data = match
					case "Mode":
						p.Mode = match
					case "VarName":
						p.Var = match
					}
				} else { // It's an UNNAMED capture group
					// For specific parsers, the first unnamed group (i=1) is the command.
					if i == 1 && (strings.HasPrefix(parserName, "linked_") || parserName == "simple_cmd") {
						p.Command = match
						captured["Command (unnamed)"] = match
					}
				}
			}

			// Fallback Command Extraction (if not set by unnamed group)
			if p.Command == "" {
				if !(strings.HasPrefix(parserName, "linked_") || parserName == "simple_cmd") {
					p.Command = strings.Split(parserName, "_")[0]
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
				p.Data = rawDatString // Assign raw string first
			}

			// Attempt to unmarshal Data if it was captured and looks like JSON
			var jsonData any
			if rawDatString != "" {
				if err := json.Unmarshal([]byte(rawDatString), &jsonData); err == nil {
					p.Data = jsonData // Overwrite Data field *only if* unmarshal succeeds
				}
				// If unmarshal fails, p.Data retains the raw string value - this is safer.
			}

			// Populate the packet struct
			return true // Successfully parsed
		}
	}
	log.Printf("Failed to parse CL2 packet: %s", message)
	return false
}

func (p *CL2Packet) Bytes() []byte {
	// Build the appropriate JSON response based on the Type field
	var reply any
	if p.Type == "sf" || p.Type == "direct" { // Wrap special feature/direct responses
		reply = &CL2Packet{
			Type: p.Type,
			Data: p.Data, // Assumes Data is already a *CL2Packet struct
			ID:   p.ID,
		}
	} else { // Standard gs, ps, ul, ru responses
		reply = &CL2Packet{
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
	return string(p.Bytes())
}

func (p *CL2Packet) DeriveProtocol() uint { return Protocol_CL2 }

func (p *CL2Packet) DeriveDialect(c *Client) uint {
	if p.origin.Handshake {
		return Dialect_CL2_Late
	} else {
		return Dialect_CL2_Early
	}
}

func (p *CL2Packet) Handler(c *Client, m *Manager) {
	p.origin = c

	switch p.Command {
	case "sh": // Special Handshake
		if c.Handshake {
			return
		}
		c.UpdateHandshake(true)
		c.JoinRoom(m.DefaultRoom)

		resp := &CL2Packet{
			Type: "direct",
			Data: &CL2Packet{
				Type: "vers",
				Data: m.ServerVersion,
			},
		}
		c.writer <- resp
		log.Printf("Client %s completed CL2 handshake", c.ID)

	case "rf": // Refresh User List
		log.Printf("Client %s requested user list refresh", c.ID)
		ulistStr := m.DefaultRoom.GenerateUserlistString()
		if ulistStr == "ul;" {
			ulistStr = ""
		} else if strings.HasSuffix(ulistStr, "ul;") {
			ulistStr = strings.TrimSuffix(ulistStr, "ul;")
		}
		resp := &CL2Packet{
			Type: "ul",
			Data: ulistStr,
		}
		c.writer <- resp

	case "set": // Set Username
		if c.NameSet {
			log.Printf("Client %s attempted to set username again", c.ID)
			return
		}
		c.SetName(p.Sender)
		log.Printf("Client %s set username to %s", c.ID, p.Sender)

		ulistStr := m.DefaultRoom.GenerateUserlistString()
		if ulistStr == "ul;" {
			ulistStr = ""
		} else if strings.HasSuffix(ulistStr, "ul;") {
			ulistStr = strings.TrimSuffix(ulistStr, "ul;")
		}
		ulistPacket := &CL2Packet{
			Type: "ul",
			Data: ulistStr,
		}
		for _, client := range m.DefaultRoom.ClientsAsSlice() {
			if client.Protocol != nil {
				switch client.Protocol.DeriveProtocol() {
				case Protocol_CL2:
					client.writer <- ulistPacket
				case Protocol_CL3or4:
					if cl3, ok := client.Protocol.(*CL3or4Packet); ok {
						cl3.SendUserlist(client, m.DefaultRoom)
					}
				}
			}
		}

	case "global": // Global Stream Message
		log.Printf("Client %s sent global message", c.ID)
		m.DefaultRoom.GmsgState = p.Data
		p.BroadcastMsg(m.DefaultRoom.ClientsAsSlice(), p.Data)

	case "private": // Private Stream Message
		log.Printf("Client %s sent private message to %s", c.ID, p.Recipient)
		if targetClient, found := m.FindClient(p.Recipient); found {
			p.UnicastMsg(targetClient, p.Data)
		} else {
			log.Printf("Target client %s not found for private message", p.Recipient)
		}

	case "l_g": // Linked Global Data/Var
		if !c.Handshake {
			return
		}
		switch p.Mode {
		case "0":
			log.Printf("Client %s sent linked global data", c.ID)
		case "1", "2":
			log.Printf("Client %s set global var '%s'", c.ID, p.Var)
			m.DefaultRoom.SetGvar(p.Var, p.Data)
			p.BroadcastVar(m.DefaultRoom.ClientsAsSlice(), p.Var, p.Data)
		}

	case "l_p": // Linked Private Data/Var
		if !c.Handshake {
			return
		}
		switch p.Mode {
		case "0":
			log.Printf("Client %s sent linked private data to %s", c.ID, p.Recipient)
		case "1", "2":
			log.Printf("Client %s set private var '%s' for %s", c.ID, p.Var, p.Recipient)
			if targetClient, found := m.FindClient(p.Recipient); found {
				p.UnicastVar(targetClient, p.Var, p.Data)
			}
		}

	case "disconnect": // Disconnect command
		log.Printf("Client %s sent disconnect command", c.ID)

	default:
		log.Printf("Unknown or unhandled CL2 Command: %s from client %s", p.Command, c.ID)
	}
}

func (p *CL2Packet) IsJSON() bool { return false }

func (p *CL2Packet) SpoofServerVersion() string { return "" }

func (p *CL2Packet) ToGeneric() *Generic {
	g := &Generic{
		Origin:  p.origin,
		Payload: p,
	}

	if p.Recipient != "" {
		g.Opcode = Generic_Unicast
	} else {
		g.Opcode = Generic_Broadcast
	}

	return g
}

func (p *CL2Packet) FromGeneric(g *Generic) {
	p.origin = g.Origin
	if p.origin != nil {
		if str, ok := p.origin.Name.(string); ok && str != "" {
			p.Sender = str
		} else {
			p.Sender = fmt.Sprintf("%v", p.origin.ID)
		}
	}

	var val, name any
	switch payload := g.Payload.(type) {
	case *CL3or4Packet:
		val = payload.Val
		name = payload.Name
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
			p.Type = "vm"
			p.Mode = "g"
			p.Var = name
		} else {
			p.Type = "gs"
		}
		p.Data = val

		if g.Target != nil && g.Target.Handshake {
			nested := &CL2Packet{
				Type: p.Type,
				Mode: p.Mode,
				Var:  p.Var,
				Data: p.Data,
			}
			p.Type = "sf"
			p.Mode = ""
			p.Var = nil
			p.Data = nested
		}
	case Generic_Unicast:
		if name != nil {
			p.Type = "vm"
			p.Mode = "p"
			p.Var = name
		} else {
			p.Type = "ps"
		}
		p.Data = val
		if g.Target != nil {
			p.ID = g.Target.ID.String()
		}

		if g.Target != nil && g.Target.Handshake {
			nested := &CL2Packet{
				Type: p.Type,
				Mode: p.Mode,
				Var:  p.Var,
				Data: p.Data,
			}
			p.Type = "sf"
			p.Mode = ""
			p.Var = nil
			p.Data = nested
		}
	}
}
