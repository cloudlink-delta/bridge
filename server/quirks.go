package server

import (
	"fmt"
	"log"
	"strings"

	"github.com/goccy/go-json"
)

func (s *Scratch_Handler) Apply_Quirks(_ *ClassicClient, p any) any {
	switch packet := p.(type) {
	case *ScratchPacket:
		// Native Scratch packets pass through natively
		// Marshal the payload value to protect against type crashing
		switch packet.Value.(type) {
		case map[string]interface{}:
			if b, err := json.Marshal(packet.Value); err == nil {
				packet.Value = string(b)
			} else {
				panic(err)
			}
		}
		return packet

	case *CL2Packet:
		log.Println("⁉️  CL2 -> Scratch translation not present")
		return nil

	case *CL4_or_CL3_Packet:
		// Cross-protocol translation: CL4/CL3 -> Scratch
		if packet.Command == "gvar" {
			return &ScratchPacket{
				Method: "set",
				Name:   packet.Name,
				Value:  packet.Value,
			}
		}
		log.Printf("⁉️  CL4/3 -> Scratch translation not present for opcode %s", packet.Command)
		return nil // Silently drop ulist, gmsg, statuscodes, etc.

	default:
		log.Printf("⁉️  Scratch translation not present for unrecognized packet %v", packet)
		return nil // Safely drop unrecognized packets instead of panicking
	}
}

func (s *CL2) Apply_Quirks(c *ClassicClient, p any) any {
	var reply CL2Packet_TxReply

	switch original := p.(type) {
	case *CL4_or_CL3_Packet:
		switch original.Command {
		case "server_version":
			reply.Type = "direct"
			reply.Data = CL2Packet_TxData{Type: "vers", Data: original.Value}

		case "ulist":
			userList := s.Get_User_List(DEFAULT_ROOM)
			var strList string
			for i, u := range userList {
				if i > 0 {
					strList += ";"
				}
				strList += fmt.Sprintf("%v", u.Username)
			}
			reply.Type = "ul"
			reply.Data = strList

		case "gmsg":
			if c.dialect == Dialect_CL2_Late {
				reply.Type = "sf"
				reply.Data = CL2Packet_TxData{Type: "gs", Data: original.Value}
			} else {
				reply.Type = "gs"
				reply.Data = original.Value
			}

		case "pmsg":
			reply.Type = "ps"
			if c.dialect == Dialect_CL2_Late {
				reply.Type = "sf"
				reply.Data = CL2Packet_TxData{Type: "ps", Data: original.Value}
			} else {
				reply.Data = original.Value
			}
			if originObj, ok := original.Origin.(*CL4_UserObject); ok {
				reply.ID = fmt.Sprintf("%v", originObj.Username)
			}

		case "linked_gmsg": // Internal command translated to CL2 lm (Linked Message)
			if c.dialect == Dialect_CL2_Late {
				reply.Type = "sf"
				reply.Data = CL2Packet_TxData{Type: "lm", Mode: "g", Data: original.Value}
			} else {
				return nil
			}

		case "linked_pmsg": // Internal command translated to CL2 lm (Linked Message)
			if c.dialect == Dialect_CL2_Late {
				reply.Type = "sf"
				reply.Data = CL2Packet_TxData{Type: "lm", Mode: "p", Data: original.Value}
				if originObj, ok := original.Origin.(*CL4_UserObject); ok {
					reply.ID = fmt.Sprintf("%v", originObj.Username)
				}
			} else {
				return nil
			}

		case "gvar":
			if c.dialect == Dialect_CL2_Late {
				reply.Type = "sf"
				reply.Data = CL2Packet_TxData{Type: "vm", Mode: "g", Var: original.Name, Data: original.Value}
			} else {
				return nil
			}

		case "pvar":
			if c.dialect == Dialect_CL2_Late {
				reply.Type = "sf"
				reply.Data = CL2Packet_TxData{Type: "vm", Mode: "p", Var: original.Name, Data: original.Value}
				if originObj, ok := original.Origin.(*CL4_UserObject); ok {
					reply.ID = fmt.Sprintf("%v", originObj.Username)
				}
			} else {
				return nil
			}

		case "direct":
			reply.Type = "direct"
			reply.Data = original.Value
			if originObj, ok := original.Origin.(*CL4_UserObject); ok {
				reply.ID = fmt.Sprintf("%v", originObj.Username)
			}

		default:
			log.Printf("⁉️  CL4/3 -> CL2 translation not present for opcode %s", original.Command)
			return nil
		}

	case *ScratchPacket:
		if original.Method == "set" || original.Method == "create" {
			if c.dialect == Dialect_CL2_Late {
				reply.Type = "sf"
				reply.Data = CL2Packet_TxData{Type: "vm", Mode: "g", Var: original.Name, Data: original.Value}
			} else {
				return nil
			}
		} else {
			log.Printf("⁉️  Scratch -> CL2 translation not present for method %s", original.Method)
			return nil
		}

	default:
		log.Printf("⁉️  CL2 translation not present for unrecognized packet %v", p)
		return nil
	}

	return reply
}

func (s *CL4_or_CL3) Apply_Quirks(c *ClassicClient, p any) any {
	var packet *CL4_or_CL3_Packet

	switch original_packet := p.(type) {
	case *CL4_or_CL3_Packet:
		// Create a shallow copy of the packet so we don't mutate the
		// broadcasted packet reference for other clients.
		clone := *original_packet
		packet = &clone

		// Cross-Protocol Translation:
		// CL2 uses internal opcodes for soft-linked P2P messages.
		// Newer protocols just see these as standard Private Messages.
		if packet.Command == "linked_gmsg" || packet.Command == "linked_pmsg" {
			packet.Command = "pmsg"
		}

	case *ScratchPacket:
		// Cross-protocol translation: Scratch -> CL4/CL3
		if original_packet.Method == "set" || original_packet.Method == "create" {
			packet = &CL4_or_CL3_Packet{
				Command: "gvar",
				Name:    original_packet.Name,
				Value:   original_packet.Value,
				Origin:  s.UserObject(c),
			}
		} else {
			log.Printf("⁉️  Scratch -> CL3/4 translation not present for method %s", original_packet.Method)
			return nil // Drop unmappable Scratch commands (like rename/delete)
		}

	default:
		// Safely drop unrecognized packets instead of crashing the server
		log.Printf("⁉️  CL3/4 translation not present for unrecognized packet %v", p)
		return nil
	}

	// SAFELY EXTRACT THE TARGET ROOM BEFORE ANY QUIRKS MODIFY IT
	targetRoom := DEFAULT_ROOM
	if packet.Rooms != nil && packet.Rooms != "" {
		if r, ok := packet.Rooms.(RoomKey); ok {
			targetRoom = r
		} else if rStr, ok := packet.Rooms.(string); ok {
			targetRoom = RoomKey(rStr)
		}
	}

	// Force_Set override to address known bugs with older CL clients
	if s.Config.Force_Set && packet.Command == "ulist" && (packet.Mode == "add" || packet.Mode == "remove") {
		packet.Mode = "set"
		packet.Value = s.Get_User_List(targetRoom)
	}

	// NOW WIPE THE ROOMS KEY FOR OLDER DIALECTS (MUST BE NIL FOR OMITEMPTY)
	if packet.Rooms != "" && c.dialect < Dialect_CL4_0_1_8 {
		log.Printf("%s 🔧 Applied Quirks: rooms context is not supported with dialects older than 0.1.8", c.GiveName())
		packet.Rooms = "" // Room contexts not supported before 0.1.8
	}

	switch packet.Command {
	case "statuscode":
		if c.dialect < Dialect_CL3_0_1_7 {
			log.Printf("%s 🔧 Applied Quirks: statuscode is not supported with dialects older than 0.1.7", c.GiveName())
			return nil // Drop unsupported command
		}

	case "server_version":
		switch c.dialect {
		case Dialect_CL3_0_1_5:
			// CL3 0.1.5 expects strict nesting with 'data' inner key
			packet.Command = "direct"
			packet.Data = map[string]any{
				"cmd":  "vers",
				"data": packet.Value,
			}
			packet.Value = nil
			log.Printf("%s 🔧 Applied Quirks: wrapping server_version with direct command for 0.1.5 dialect", c.GiveName())

		case Dialect_CL3_0_1_7:
			// CL3 0.1.7 expects nesting with 'val' inner key
			packet.Command = "direct"
			packet.Value = map[string]any{
				"cmd": "vers",
				"val": packet.Value,
			}
			log.Printf("%s 🔧 Applied Quirks: using vers with value for server_version command for 0.1.7 dialect", c.GiveName())
		}

	case "motd":
		switch c.dialect {
		case Dialect_CL3_0_1_5:
			log.Printf("%s 🔧 Applied Quirks: MOTD is not supported on the 0.1.5 dialect", c.GiveName())
			return nil // 0.1.5 does not support MOTD
		case Dialect_CL3_0_1_7:
			packet.Command = "direct"
			packet.Value = map[string]any{
				"cmd": "motd",
				"val": packet.Value,
			}
			log.Printf("%s 🔧 Applied Quirks: patching MOTD for 0.1.7 dialect", c.GiveName())
		}

	case "client_obj":
		if c.dialect < Dialect_CL4_0_2_0 {
			log.Printf("%s 🔧 Applied Quirks: client_obj is not supported with dialects older than 0.2.0", c.GiveName())
			return nil // client_obj is a 0.2.0+ specific feature
		}

	case "ulist":
		if c.dialect < Dialect_CL4_0_2_0 {
			log.Printf("%s 🔧 Applied Quirks: patching ulist reply for dialects older than 0.2.0", c.GiveName())

			if c.dialect < Dialect_CL4_0_1_8 {

				// 0.1.5 and 0.1.7 DO NOT support differential updates.
				packet.Mode = ""

				var userList []*CL4_UserObject
				if list, ok := packet.Value.([]*CL4_UserObject); ok {
					userList = list
				} else {
					userList = s.Get_User_List(targetRoom)
				}

				// Extract names safely
				var sb strings.Builder
				first := true
				for _, u := range userList {
					if u.Username != nil && u.Username != "" { // Avoid inserting blanks
						if !first {
							sb.WriteString(";")
						}
						sb.WriteString(fmt.Sprintf("%v", u.Username))
						first = false
					}
				}
				packet.Value = sb.String() + ";"

			} else {
				// 0.1.8 and 0.1.9 DO support differential updates (mode: "set", "add", "remove")
				// However, they expect Strings or Slices of Strings, not UserObjects.
				switch packet.Mode {
				case "set":
					if userList, ok := packet.Value.([]*CL4_UserObject); ok {
						strSlice := make([]any, len(userList))
						for i, u := range userList {
							strSlice[i] = u.Username
						}
						packet.Value = strSlice
					}
				case "add", "remove":
					if userObj, ok := packet.Value.(*CL4_UserObject); ok {
						packet.Value = userObj.Username
					}
				}
			}
		}

	case "gmsg", "gvar", "pmsg", "pvar", "direct", "linked_gmsg", "linked_pmsg":
		if c.dialect < Dialect_CL4_0_1_8 {
			log.Printf("%s 🔧 Applied Quirks: rooms context is not supported with dialects older than 0.1.8", c.GiveName())
			packet.Rooms = nil // MUST be nil so `omitempty` removes the key completely!
		}

		// origin object handling
		if c.dialect < Dialect_CL4_0_1_8 {

			// 0.1.8 and 0.1.9 DO support UserObject origins.
			// We only downgrade for 0.1.7 and older.

			if originObj, ok := packet.Origin.(*CL4_UserObject); ok {
				if c.dialect == Dialect_CL3_0_1_7 {
					log.Printf("%s 🔧 Applied Quirks: downgrading origin object to string for 0.1.7 dialect", c.GiveName())
					packet.Origin = originObj.Username
				} else {
					log.Printf("%s 🔧 Applied Quirks: packet origin is not supported with 0.1.5", c.GiveName())
					packet.Origin = nil // Not supported in 0.1.5
				}
			}
		}
	}
	return packet
}
