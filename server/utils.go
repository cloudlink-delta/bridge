package server

import (
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/snowflake"
	"github.com/goccy/go-json"
	"github.com/google/uuid"
)

func (m *Manager) Unicast(client *Client, packet any) {
	if proto, ok := packet.(Protocol); ok {
		original := proto.DeriveProtocol()
		target := client.Protocol.DeriveProtocol()
		if client.Protocol != nil && original != target {

			// "Converting x to y"
			log_message := &strings.Builder{}
			log_message.WriteString(" 𝐢 Converting ")
			switch original {
			case Protocol_CL2:
				log_message.WriteString("CL2")
			case Protocol_CL3or4:
				log_message.WriteString("CL3/CL4")
			case Protocol_Scratch:
				log_message.WriteString("Scratch")
			}
			log_message.WriteString(" to ")
			switch target {
			case Protocol_CL2:
				log_message.WriteString("CL2")
			case Protocol_CL3or4:
				log_message.WriteString("CL3/CL4")
			case Protocol_Scratch:
				log_message.WriteString("Scratch")
			}
			log.Println(log_message.String())

			// Translate the message
			gen := proto.ToGeneric()

			// Abort if the translation is a no-op
			if gen.Opcode == Generic_NOOP {
				return
			}

			// Prepare the translation
			gen.Target = client
			translated := client.Protocol.New()
			translated.FromGeneric(gen)

			// Write the translated message
			client.writer <- translated
			return
		}
	}

	// Don't translate the message if they are the same
	client.writer <- packet
}

func (m *Manager) BroadcastToRoom(room *Room, packet any, exclusions ...*Client) {
	m.Multicast(room.Clients, packet, exclusions...)
}

func (m *Manager) Multicast(clients map[snowflake.ID]*Client, packet any, exclusions ...*Client) {
	clients = Filter(clients, exclusions...)
	for _, client := range clients {
		go m.Unicast(client, packet)
	}
}

func (m *Manager) IsUsernameTaken(name string, excludeID snowflake.ID) bool {
	m.lock.Lock()
	defer m.lock.Unlock()
	for id, client := range m.connections {
		if id != excludeID && client.NameSet && client.Name == name {
			return true
		}
	}
	return false
}

func (m *Manager) FindClient(id any) (*Client, bool) {
	m.lock.Lock()
	defer m.lock.Unlock()

	// Try finding by snowflake ID first
	if sfID, ok := id.(snowflake.ID); ok {
		client, found := m.connections[sfID]
		return client, found
	}

	// Try finding by username
	idStr := fmt.Sprintf("%v", id) // Convert id to string for comparison
	for _, client := range m.connections {
		if client.NameSet && fmt.Sprintf("%v", client.Name) == idStr {
			return client, true
		}
	}

	// Try finding by UUID
	if uuidVal, err := uuid.Parse(idStr); err == nil {
		for _, client := range m.connections {
			if client.UUID == uuidVal {
				return client, true
			}
		}
	}

	return nil, false
}

func Filter(source map[snowflake.ID]*Client, exclusions ...*Client) map[snowflake.ID]*Client {
	filtered := make(map[snowflake.ID]*Client)
	for id, client := range source {
		found := false
		for _, exclusion := range exclusions {
			if client.ID == exclusion.ID {
				found = true
				break
			}
		}
		if !found {
			filtered[id] = client
		}
	}
	return filtered
}

func isTypeDeclaration(val any) bool {
	if valMap, ok := val.(map[string]any); ok {
		if cmd, ok := valMap["cmd"].(string); ok && cmd == "type" {
			return true
		}
	}
	return false
}

// Helper to validate username/room name types
func validateUsername(name any) error {
	switch name.(type) {
	case string, int64, float64, bool:
		return nil // Valid types
	default:
		return fmt.Errorf("username/room value must be a string, boolean, float, or int")
	}
}

// Helper to convert single value or slice/array to []any
func ToSlice(value any) ([]any, error) {
	switch v := value.(type) {
	case nil:
		return []any{}, nil // Empty slice for nil
	case []any:
		return v, nil // Already a slice
	case string, int64, float64, bool:
		return []any{v}, nil // Single element slice
	default:
		// Try unmarshalling if it's a string that might be a JSON array
		if strVal, ok := v.(string); ok {
			var sliceVal []any
			if err := json.Unmarshal([]byte(strVal), &sliceVal); err == nil {
				return sliceVal, nil
			}
		}
		return nil, fmt.Errorf("value is not a slice, array, or single valid element")
	}
}
