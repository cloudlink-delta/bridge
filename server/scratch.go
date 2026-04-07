package server

import (
	"log"

	"github.com/goccy/go-json"
	"github.com/kaptinlin/jsonschema"
)

func NewScratchPacket(c *Client) Protocol {
	return &ScratchPacket{origin: c}
}

func (p *ScratchPacket) New() Protocol {
	return &ScratchPacket{origin: p.origin}
}

type ScratchPacket struct {
	Method    string `json:"method" jsonschema:"required"`
	ProjectID string `json:"project_id,omitempty"`
	User      string `json:"user,omitempty"`
	Name      any    `json:"name,omitempty"`
	NewName   any    `json:"new_name,omitempty"`
	Value     any    `json:"value,omitempty"`

	origin  *Client `json:"-"`
	dialect uint    `json:"-"`
}

func (p *ScratchPacket) BroadcastMsg(clients []*Client, data any) {}

func (p *ScratchPacket) BroadcastVar(clients []*Client, name any, val any) {}

func (p *ScratchPacket) UnicastMsg(client *Client, data any) {}

func (p *ScratchPacket) UnicastVar(client *Client, name any, val any) {}

func (p *ScratchPacket) BroadcastBinder() chan any { return nil }

func (p *ScratchPacket) UnicastBinder() chan any { return nil }

func (p *ScratchPacket) Reader(data []byte) bool {
	if !json.Valid(data) {
		log.Println("Scratch Reader: Invalid JSON received")
		return false
	}

	// Schema validation (using jsonschema)
	schema := jsonschema.FromStruct[ScratchPacket]()
	result := schema.Validate(data)
	if !result.IsValid() {
		log.Printf("Scratch Packet failed schema validation: %v", result)
		return false
	}

	// Unmarshal into the struct
	if err := json.Unmarshal(data, &p); err != nil {
		log.Printf("Scratch JSON Unmarshal Error: %s", err)
		return false
	}

	// Basic check: Method must exist
	if p.Method == "" {
		log.Println("Scratch Reader: Packet missing required 'method' field")
		return false
	}

	return true
}

func (p *ScratchPacket) Bytes() []byte {
	b, err := json.Marshal(p)
	if err != nil {
		log.Println(err)
		return nil
	}
	return b
}

func (p *ScratchPacket) String() string { return string(p.Bytes()) }

func (p *ScratchPacket) DeriveProtocol() uint { return Protocol_Scratch }

func (p *ScratchPacket) DeriveDialect(c *Client) uint { return Dialect_Undefined }

func (p *ScratchPacket) Handler(c *Client, m *Manager) {
	switch p.Method {
	case "handshake":
		if c.Handshake {
			return
		}
		c.SetName(p.User)
		project := m.CreateRoom(p.ProjectID)
		c.JoinRoom(project)
		c.UpdateHandshake(true)
		for name, value := range project.GetAllGvars() {
			m.Unicast(c, &ScratchPacket{
				Method: "set",
				Name:   name,
				Value:  value,
			})
		}

	case "set":
		if !c.Handshake {
			return
		}
		project := c.AllRooms()[0]
		project.SetGvar(p.Name, p.Value)
		m.BroadcastToRoom(project, &ScratchPacket{
			Method: "set",
			Name:   p.Name,
			Value:  p.Value,
		})

	case "create":
		if !c.Handshake {
			return
		}
		project := c.AllRooms()[0]
		project.SetGvar(p.Name, p.Value)

		m.BroadcastToRoom(project, &ScratchPacket{
			Method: "create",
			Name:   p.Name,
			Value:  p.Value,
		})

	case "rename":
		if !c.Handshake {
			return
		}
		project := c.AllRooms()[0]
		old := project.GetGvar(p.Name)
		project.SetGvar(p.NewName, old)
		project.DeleteGvar(p.Name)

		m.BroadcastToRoom(project, &ScratchPacket{
			Method:  "rename",
			Name:    p.Name,
			NewName: p.NewName,
		})

	case "delete":
		if !c.Handshake {
			return
		}
		project := c.AllRooms()[0]
		project.DeleteGvar(p.Name)

		m.BroadcastToRoom(project, &ScratchPacket{
			Method: "delete",
			Name:   p.Name,
		})

	default:
		log.Println("Unknown Scratch Method")
	}
}

func (p *ScratchPacket) IsJSON() bool { return true }

func (p *ScratchPacket) SpoofServerVersion() string { return "" }

func (p *ScratchPacket) ToGeneric() *Generic {
	g := &Generic{
		Origin:  p.origin,
		Payload: p,
	}

	switch p.Method {
	case "set", "create", "rename", "delete":
		g.Opcode = Generic_Broadcast
	default:
		g.Opcode = Generic_NOOP
	}

	return g
}

func (p *ScratchPacket) FromGeneric(g *Generic) {
	p.origin = g.Origin

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
		p.Method = "set"
		if name != nil {
			p.Name = name
		} else {
			p.Name = "message" // Scratch expects variables, use a fallback variable name if it's a generic message
		}
		p.Value = val
	}
}
