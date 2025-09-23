package cloudlink

import (
	"log"
	"regexp"
	"strings"

	"github.com/goccy/go-json"
)

// CL2PacketFormatter holds the parsing logic.
type CL2PacketFormatter struct {
	parsers map[string]*regexp.Regexp
}

// NewCL2PacketFormatter creates and initializes a new packet formatter with compiled regex.
func NewCL2PacketFormatter() *CL2PacketFormatter {
	parsers := make(map[string]*regexp.Regexp)
	// Using named capture groups to easily extract parameters
	parsers["set_username"] = regexp.MustCompile(`^<%sn>\n(?P<Sender>.*)$`)
	parsers["disconnect"] = regexp.MustCompile(`^<%ds>\n(?P<Sender>.*)$`)
	parsers["simple_cmd"] = regexp.MustCompile(`^<%(rf|sh|rt)>\n?(?P<Sender>.*)$`)
	parsers["global_stream"] = regexp.MustCompile(`^<%gs>\n(?P<Sender>.*?)\n(?s)(?P<Data>.*)$`)
	parsers["private_stream"] = regexp.MustCompile(`^<%ps>\n(?P<Sender>.*?)\n(?P<Recipient>.*?)\n(?s)(?P<Data>.*)$`)
	parsers["linked_data"] = regexp.MustCompile(`^<%(l_g|l_p)>\n(?P<Mode>0)\n(?P<Sender>.*?)\n(?P<Recipient>.*?)\n(?s)(?P<Data>.*)$`)
	parsers["linked_vars"] = regexp.MustCompile(`^<%(l_g|l_p)>\n(?P<Mode>[1-2])\n(?P<Sender>.*?)\n(?P<RecipientVar>.*?)\n(?P<DataVar>.*?)\n(?s)(?P<Data>.*)$`)

	return &CL2PacketFormatter{parsers: parsers}
}

// Parse takes a raw message string and returns a structured PacketCL2.
func (h *CL2PacketFormatter) Parse(message string) (*PacketCL2, bool) {
	for key, re := range h.parsers {
		if re.MatchString(message) {
			matches := re.FindStringSubmatch(message)
			names := re.SubexpNames()
			packet := &PacketCL2{} // Renamed Packet to PacketCL2

			// Populate packet struct from named capture groups
			for i, name := range names {
				if i > 0 && i < len(matches) {
					switch name {
					case "Sender":
						packet.Sender = matches[i]
					case "Recipient":
						packet.Recipient = matches[i]
					case "Data":
						packet.Data = matches[i]
					case "Mode":
						packet.Mode = matches[i]
					case "RecipientVar":
						if packet.Mode == "1" {
							packet.Recipient = matches[i]
						} else {
							packet.VarName = matches[i]
						}
					case "DataVar":
						if packet.Mode == "1" {
							packet.VarName = matches[i]
						}
					}
				}
			}

			// Extract the command
			if key == "simple_cmd" || key == "linked_data" || key == "linked_vars" {
				packet.Command = re.FindStringSubmatch(message)[1]
			} else {
				packet.Command = strings.Split(key, "_")[0]
			}

			return packet, true
		}
	}
	return nil, false // No match found
}

// BuildGlobalResponse creates a JSON packet for a global stream update.
func BuildGlobalResponse(data string, isSpecialClient bool) ([]byte, error) {
	if isSpecialClient {
		resp := CL2Response{
			Type: "sf",
			Data: CL2DataPayload{Type: "gs", Data: data},
		}
		return json.Marshal(resp)
	}
	resp := CL2SimpleReply{Type: "gs", Data: data}
	return json.Marshal(resp)
}

// BuildPrivateResponse creates a JSON packet for a private stream update.
func BuildPrivateResponse(data, recipient string, isSpecialClient bool) ([]byte, error) {
	if isSpecialClient {
		resp := CL2Response{
			Type: "sf",
			ID:   recipient,
			Data: CL2DataPayload{Type: "ps", Data: data},
		}
		return json.Marshal(resp)
	}
	resp := CL2SimpleReply{Type: "ps", Data: data, ID: recipient}
	return json.Marshal(resp)
}

// BuildUserListResponse creates a JSON packet for a user list update.
func BuildUserListResponse(users []string) ([]byte, error) {
	resp := CL2SimpleReply{
		Type: "ul",
		Data: strings.Join(users, ";") + ";",
	}
	return json.Marshal(resp)
}

// BuildVariableResponse creates a JSON packet for a named variable update.
func BuildVariableResponse(mode, recipient, varName, data string) ([]byte, error) {
	resp := CL2Response{
		Type: "sf",
		ID:   recipient,
		Data: CL2DataPayload{
			Type: "vm",
			Mode: mode, // "g" or "p"
			Var:  varName,
			Data: data,
		},
	}
	return json.Marshal(resp)
}

// CL2HandleMessage
func CL2HandleMessage(client *Client, msg string) {
	handler := NewCL2PacketFormatter()
	manager := client.manager
	defaultRoom := client.rooms["default"]

	if defaultRoom == nil {
		panic("Default room not found")
	}

	if packet, ok := handler.Parse(msg); ok {
		switch packet.Command {

		case "set":
			// Update the client object with the username
			client.Lock()
			client.username = packet.Sender
			client.Unlock()

			log.Println("New username:", client.username)

			// Announce the new user list to the default room
			allUsers := getAllUsernamesInRoom(defaultRoom)
			response, _ := BuildUserListResponse(allUsers)
			MulticastMessage(defaultRoom.clients, response)

		case "gs":
			// Update the global message state within the room
			defaultRoom.gmsgStateMutex.Lock()
			defaultRoom.gmsgState = packet.Data
			defaultRoom.gmsgStateMutex.Unlock()

			// Get client groups from the default room
			specialClients, standardClients := getClientGroupsByHandshake(defaultRoom)

			// Build and multicast to special-feature clients
			if len(specialClients) > 0 {
				specialResponse, _ := BuildGlobalResponse(packet.Data, true)
				MulticastMessage(specialClients, specialResponse)
			}

			// Build and multicast to standard clients
			if len(standardClients) > 0 {
				standardResponse, _ := BuildGlobalResponse(packet.Data, false)
				MulticastMessage(standardClients, standardResponse)
			}

		case "ps":
			// Find the target client within the entire manager
			if targetClient, found := findClientByUsername(manager, packet.Recipient); found {
				// Build response based on the TARGET's handshake status and unicast
				response, _ := BuildPrivateResponse(packet.Data, packet.Recipient, targetClient.handshake)
				UnicastMessage(targetClient, response)
			}

		default:
			log.Printf("Unknown CL2 command: %s", packet.Command)

		}

	} else {
		panic("Failed to parse CL2 packet")
	}
}
