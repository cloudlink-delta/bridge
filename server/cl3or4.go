package cloudlink

import (
	"log"

	"github.com/bwmarrin/snowflake"
	"github.com/google/uuid"
	"github.com/kaptinlin/jsonschema"

	"github.com/goccy/go-json"
)

type CL3or4Packet struct {
	Command   string `json:"cmd" jsonschema:"required"`
	Name      any    `json:"name,omitempty"`
	Val       any    `json:"val,omitempty"`
	ID        any    `json:"id,omitempty"`
	Rooms     any    `json:"rooms,omitempty"`
	Listener  any    `json:"listener,omitempty"`
	Code      string `json:"code,omitempty"`
	CodeID    int    `json:"code_id,omitempty"`
	Mode      string `json:"mode,omitempty"`
	Origin    any    `json:"origin,omitempty"`
	Details   any    `json:"details,omitempty"`
	Recipient any    `json:"recipient,omitempty"`
}

type UserObject struct {
	ID       snowflake.ID `json:"id,omitempty"`
	Username any          `json:"username,omitempty"`
	UUID     uuid.UUID    `json:"uuid,omitempty"`
}

func (p *CL3or4Packet) DeriveProtocol() uint {
	return Protocol_CL3or4
}

func (p *CL3or4Packet) DeriveDialect(c *Client) uint {

	if c.dialect == Dialect_Undefined {
		if p.Command == "handshake" {

			// Check for the new v0.2.0 handshake format
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
	}

	return c.dialect
}

func (p *CL3or4Packet) IsJSON() bool {
	return true
}

// Takes a raw byte array and returns true if it is a structured CL3 or CL4 packet.
// Also populates the provided packet struct with the extracted parameters.
func (p *CL3or4Packet) Reader(data []byte) bool {
	schema := jsonschema.FromStruct[CL3or4Packet]()
	result := schema.Validate(data).IsValid()
	if !result {
		return false
	}
	if err := json.Unmarshal(data, &p); err != nil {
		panic(err)
	}
	return true
}

func (p *CL3or4Packet) String() string {
	return string(p.Bytes())
}

func (p *CL3or4Packet) Bytes() []byte {
	b, err := json.Marshal(p)
	if err != nil {
		log.Println(err)
		return nil
	}
	return b
}

func (p *CL3or4Packet) Handler(c *Client, m *Manager) {

	if p.Command == "direct" && c.dialect == Dialect_CL3_0_1_7 {
		// Check if we can de-nest the "direct" command's value
		log.Println("De-nesting 'direct' command value...")
		if p.Val.(map[string]any)["cmd"] != nil {
			p.Command = p.Val.(map[string]any)["cmd"].(string)
			p.Val = p.Val.(map[string]any)["val"]
		}
	}

	switch p.Command {
	case "handshake":
		if c.Handshake {
			return
		}
		c.UpdateHandshake(true)
		c.JoinRoom(m.DefaultRoom)
		p.SendHandshake(c, m)
		p.SendStatuscode(c, "I:100 | OK", 100, nil, nil)
	case "gmsg":
		r := c.Rooms
		for _, room := range r {
			m.BroadcastToRoom(room, &CL3or4Packet{
				Command: "gmsg",
				Name:    p.Name,
				Val:     p.Val,
				Origin:  c.GetUserObject(),
			})
		}
	case "gvar":
	case "pvar":
		if !c.NameSet {
			p.SendStatuscode(c, "E:111 | ID required", 111, nil, nil)
			return
		}
	case "pmsg":
		if !c.NameSet {
			p.SendStatuscode(c, "E:111 | ID required", 111, nil, nil)
			return
		}
	case "setid":
		if c.NameSet {
			p.SendStatuscode(c, "E:107 | ID already set", 107, nil, nil)
			return
		}

		// Client does not support the handshake command
		if c.dialect < Dialect_CL3_0_1_7 {
			c.UpdateHandshake(true)
			c.JoinRoom(m.DefaultRoom)
		}

		c.SetName(p.Val)
		p.SendUserlist(c, m.DefaultRoom)
		p.SendStatuscode(c, "I:100 | OK", 100, nil, c.GetUserObject())
	case "link":
		if !c.NameSet {
			p.SendStatuscode(c, "E:111 | ID required", 111, nil, nil)
			return
		}
	case "unlink":
		if !c.NameSet {
			p.SendStatuscode(c, "E:111 | ID required", 111, nil, nil)
			return
		}
	case "direct":
		if p.Recipient != "" && !c.NameSet {
			p.SendStatuscode(c, "E:111 | ID required", 111, nil, nil)
			return
		}
	case "echo":
		c.writer <- p
	default:
		log.Println("Unknown CL Method")
	}
}

func (p *CL3or4Packet) SendHandshake(c *Client, m *Manager) {

	// Report IP address back to client
	ip_resp := &CL3or4Packet{
		Command: "client_ip",
		Val:     c.conn.RemoteAddr().String(),
	}
	c.writer <- ip_resp

	// Send server version
	if c.dialect <= Dialect_CL3_0_1_7 {

		// CL3 expects the version within a "direct" command
		c.writer <- &CL3or4Packet{
			Command: "direct",
			Val: map[string]any{
				"cmd": "vers",
				"val": c.SpoofServerVersion(),
			},
		}

	} else {

		// CL4 expects a simple top-level command
		c.writer <- &CL3or4Packet{
			Command: "server_version",
			Val:     c.SpoofServerVersion(),
		}
	}

	// Send MOTD
	c.writer <- &CL3or4Packet{
		Command: "motd",
		Val:     m.ServerVersion,
	}

	// Send client object (Only for latest CL4 clients)
	if c.dialect >= Dialect_CL4_0_2_0 {
		c.writer <- &CL3or4Packet{
			Command: "client_obj",
			Val:     c.GetUserObject(),
		}
	}

	// Send initial state for all subscribed rooms
	for _, room := range c.Rooms {

		// Send gmsg state
		c.writer <- &CL3or4Packet{
			Command: "gmsg",
			Val:     room.GmsgState,
			Rooms:   room.Name,
		}

		// Send userlist state (dialect-specific)
		p.SendUserlist(c, room)

		// Send gvar state
		for name, value := range room.GvarStates {
			c.writer <- &CL3or4Packet{
				Command: "gvar",
				Name:    name,
				Val:     value,
				Rooms:   room.Name,
			}
		}
	}
}

func (p *CL3or4Packet) SendUserlist(c *Client, room *Room) {
	if c.dialect < Dialect_CL4_0_1_8 {

		// Very old clients expect a semicolon-separated string of usernames
		c.writer <- &CL3or4Packet{
			Command: "ulist",
			Val:     room.GenerateUserlistString(),
		}

	} else {

		if c.dialect == Dialect_CL4_0_2_0 {

			// Latest clients expect an array of user objects and a "set" mode
			c.writer <- &CL3or4Packet{
				Command: "ulist",
				Mode:    "set",
				Val:     room.GenerateUserObjectList(),
				Rooms:   room.Name,
			}

		} else {

			// Older clients expect an array of username strings
			c.writer <- &CL3or4Packet{
				Command: "ulist",
				Val:     room.GenerateUserlistSlice(),
				Rooms:   room.Name,
			}
		}
	}
}

func (p *CL3or4Packet) SendStatuscode(c *Client, code string, codeID int, details any, val any) {

	// Early CL3 dialects do not support status codes, so we don't send anything.
	if c.dialect < Dialect_CL3_0_1_7 {
		return
	}

	c.writer <- &CL3or4Packet{
		Command:  "statuscode",
		Code:     code,
		CodeID:   codeID,
		Details:  details,
		Val:      val,
		Listener: p.Listener,
	}
}

func (p *CL3or4Packet) BroadcastMsg(clients []*Client, val any) {
	for _, c := range clients {
		c.writer <- &CL3or4Packet{
			Command: "gmsg",
			Val:     val,
		}
	}
}

func (p *CL3or4Packet) BroadcastVar(clients []*Client, name string, val any) {
	for _, c := range clients {
		c.writer <- &CL3or4Packet{
			Command: "gvar",
			Name:    name,
			Val:     val,
		}
	}
}

func (p *CL3or4Packet) UnicastMsg(c *Client, val any) {
	c.writer <- &CL3or4Packet{
		Command: "pmsg",
		Val:     val,
	}
}

func (p *CL3or4Packet) UnicastVar(c *Client, name string, val any) {
	c.writer <- &CL3or4Packet{
		Command: "pvar",
		Name:    name,
		Val:     val,
	}
}
