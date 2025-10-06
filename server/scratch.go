package cloudlink

import (
	"log"

	"github.com/goccy/go-json"
	"github.com/kaptinlin/jsonschema"
)

type ScratchPacket struct {
	Method    string `json:"method" jsonschema:"required"`
	ProjectID string `json:"project_id,omitempty"`
	User      string `json:"user,omitempty"`
	Name      any    `json:"name,omitempty"`
	NewName   any    `json:"new_name,omitempty"`
	Value     any    `json:"value,omitempty"`
}

func (s *ScratchPacket) DeriveProtocol() uint {
	return Protocol_Scratch
}

func (s *ScratchPacket) DeriveDialect(_ *Client) uint {
	return Dialect_Undefined
}

func (s *ScratchPacket) IsJSON() bool {
	return true
}

func (s *ScratchPacket) Reader(data []byte) bool {
	schema := jsonschema.FromStruct[ScratchPacket]()
	result := schema.Validate(data).IsValid()
	if !result {
		return false
	}
	if err := json.Unmarshal(data, &s); err != nil {
		panic(err)
	}
	return true
}

func (s *ScratchPacket) Bytes() []byte {
	b, err := json.Marshal(s)
	if err != nil {
		log.Println(err)
		return nil
	}
	return b
}

func (s *ScratchPacket) String() string {
	return string(s.Bytes())
}

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
