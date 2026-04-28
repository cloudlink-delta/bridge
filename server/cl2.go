package server

import (
	"regexp"
	"strings"
	"sync"

	"github.com/goccy/go-json"
)

// Define a struct to hold regex patterns with their names for ordered matching.
type cl2Parser struct {
	name string
	re   *regexp.Regexp
}

// CL2 implements the Protocol interface
type CL2 struct {
	*Server
	parsers []cl2Parser
	links   map[*BridgeClient]string // Stores the CL2 "Soft Link"
	linksMu sync.RWMutex             // Mutex for thread-safe map access
}

func New_CL2(parent *Server) Protocol {
	return &CL2{
		Server: parent,
		links:  make(map[*BridgeClient]string),
		parsers: []cl2Parser{
			{
				name: "linked_p_vars",
				re:   regexp.MustCompile(`^<%(l_p)>\s*\n(?P<Mode>[1-2])\s+(?P<Sender>[^\n]*)\s+(?P<Recipient>[^\n]*)\s+(?P<VarName>[^\n]*)\s+(?s)(?P<Data>.*)$`),
			},
			{
				name: "linked_g_vars",
				re:   regexp.MustCompile(`^<%(l_g)>\s*\n(?P<Mode>[1-2])\s+(?P<Sender>[^\n]*)\s+(?P<VarName>[^\n]*)\s+(?s)(?P<Data>.*)$`),
			},
			{
				name: "linked_p_data",
				re:   regexp.MustCompile(`^<%(l_p)>\s*\n(?P<Mode>0)\s+(?P<Sender>[^\n]*)\s+(?P<Recipient>[^\n]*)\s+(?s)(?P<Data>.*)$`),
			},
			{
				name: "private_stream",
				re:   regexp.MustCompile(`^<%ps>\s*\n(?P<Sender>[^\n]*)\s+(?P<Recipient>[^\n]*)\s+(?s)(?P<Data>.*)$`),
			},
			{
				name: "global_stream",
				re:   regexp.MustCompile(`^<%gs>\s*\n(?P<Sender>[^\n]*)\s+(?s)(?P<Data>.*)$`),
			},
			{
				name: "linked_g_data",
				re:   regexp.MustCompile(`^<%(l_g)>\s*\n(?P<Mode>0)\s+(?P<Sender>[^\n]*)\s+(?s)(?P<Data>.*)$`),
			},
			{
				name: "set_username",
				re:   regexp.MustCompile(`^<%sn>\s*\n(?P<Sender>[^\n]*)$`),
			},
			{
				name: "disconnect",
				re:   regexp.MustCompile(`^<%ds>\s*\n(?P<Sender>[^\n]*)$`),
			},
			{
				name: "simple_cmd",
				re:   regexp.MustCompile(`^<%(rf|sh)>\s*\n?(?P<Sender>[^\n]*)$`),
			},
			{
				name: "linker",
				re:   regexp.MustCompile(`^<%(rt|rl)>\s*\n?(?P<Sender>[^\n]*)\n+(?P<Recipient>[^\n]*)$`),
			},
		},
	}
}

func (s *CL2) On_Disconnect(c *BridgeClient, rooms RoomKeys) {
	s.linksMu.Lock()
	delete(s.links, c)
	s.linksMu.Unlock()

	if c.Username == nil || c.Username == "" {
		return
	}

	userObj := s.UserObject(c)

	for _, room := range rooms { // <--- Loop over `rooms` param
		s.Broadcast(room, &Common_Packet{
			Command: "ulist",
			Mode:    "remove",
			Value:   userObj,
			Rooms:   room,
		}, c)
	}
}

func (s *CL2) Reader(client *BridgeClient, data []byte) bool {
	message := string(data)
	if !strings.HasPrefix(message, "<%") {
		return false
	}

	p := &CL2Packet{}
	parsed := false

	for _, parser := range s.parsers {
		if parser.re.MatchString(message) {
			matches := parser.re.FindStringSubmatch(message)
			names := parser.re.SubexpNames()

			captured := make(map[string]string)
			for i := 1; i < len(matches); i++ {
				if names[i] != "" {
					captured[names[i]] = matches[i]
					switch names[i] {
					case "Sender":
						p.Sender = matches[i]
					case "Recipient":
						p.Recipient = matches[i]
					case "Mode":
						p.Mode = matches[i]
					case "VarName":
						p.Var = matches[i]
					}
				} else if i == 1 && (strings.HasPrefix(parser.name, "linked_") || parser.name == "simple_cmd" || parser.name == "linker") {
					p.Command = matches[i]
				}
			}

			if p.Command == "" {
				p.Command = strings.Split(parser.name, "_")[0]
			}

			if rawDatString, ok := captured["Data"]; ok {
				var jsonData any
				if err := json.Unmarshal([]byte(rawDatString), &jsonData); err == nil {
					p.Data = jsonData
				} else {
					p.Data = rawDatString
				}
			}
			parsed = true
			break
		}
	}

	if !parsed {
		s.Logger.Error().Msgf("Failed to parse CL2 packet: %s", message)
		return false
	}

	go s.Handler(client, p)
	return true
}

func (s *CL2) Handler(client *BridgeClient, p *CL2Packet) {
	if client == nil || client.Conn == nil {
		return
	}

	s.Logger.Debug().Any("packet", p).Any("client", client).Msg("Received CL2 packet")

	if client.Conn != nil {
		s.classicclientsmu.RLock()
		active := s.ClassicClients[client]
		s.classicclientsmu.RUnlock()
		if !active {
			return
		}
	}

	if client.dialect == Dialect_Undefined {
		if p.Command == "sh" {
			s.Upgrade_Dialect(client, Dialect_CL2_Late)
		} else {
			s.Upgrade_Dialect(client, Dialect_CL2_Early)
		}
	}

	switch p.Command {
	case "sh":
		s.Unicast(client, &Common_Packet{Command: "server_version", Value: "0.1.5"})
		s.Unicast(client, &Common_Packet{
			Command: "ulist",
			Mode:    "set",
			Value:   s.Get_User_List(DEFAULT_ROOM),
			Rooms:   DEFAULT_ROOM,
		})

	case "rl": // Create Soft Link
		s.linksMu.Lock()
		s.links[client] = p.Recipient
		s.linksMu.Unlock()

	case "rt": // Undo Soft Link
		s.linksMu.Lock()
		delete(s.links, client)
		s.linksMu.Unlock()

	case "set", "sn":
		if client.Username != nil && client.Username != "" {
			return
		}
		client.Username = p.Sender

		s.Unicast(client, &Common_Packet{
			Command: "ulist",
			Mode:    "set",
			Value:   s.Get_User_List(DEFAULT_ROOM),
			Rooms:   DEFAULT_ROOM,
		})

		s.Broadcast(DEFAULT_ROOM, &Common_Packet{
			Command: "ulist",
			Mode:    "add",
			Value:   s.UserObject(client),
			Rooms:   DEFAULT_ROOM,
		}, client)

	case "rf":
		s.Unicast(client, &Common_Packet{
			Command: "ulist",
			Mode:    "set",
			Value:   s.Get_User_List(DEFAULT_ROOM),
			Rooms:   DEFAULT_ROOM,
		})

	case "gs", "global":
		s.Broadcast(DEFAULT_ROOM, &Common_Packet{
			Command: "gmsg",
			Value:   p.Data,
			Rooms:   DEFAULT_ROOM,
			Origin:  s.UserObject(client),
		})

	case "ps", "private":
		if client.Username == nil || client.Username == "" {
			return
		}
		targets := s.Get_Clients(DEFAULT_ROOM, p.Recipient)
		s.Multicast(&Common_Packet{
			Command: "pmsg",
			Value:   p.Data,
			Origin:  s.UserObject(client),
		}, targets)

	case "l_g": // Linked Global
		switch p.Mode {
		case "0":
			// Mode 0: Linked Global Data -> Multicast to the user stored in Soft Link
			s.linksMu.RLock()
			targetID, exists := s.links[client]
			s.linksMu.RUnlock()

			if exists && targetID != "" {
				targets := s.Get_Clients(DEFAULT_ROOM, targetID)
				s.Multicast(&Common_Packet{
					Command: "linked_gmsg", // Internal cross-protocol command
					Value:   p.Data,
					Origin:  s.UserObject(client),
				}, targets)
			}
		case "1":
			// Mode 1: Standard Global Variable Broadcast
			s.SetRoomGlobalVar(client, DEFAULT_ROOM, p.Var, p.Data)
			s.Broadcast(DEFAULT_ROOM, &Common_Packet{
				Command: "gvar",
				Name:    p.Var,
				Value:   p.Data,
				Origin:  s.UserObject(client),
			})
		case "2":
			// Mode 2: Directed Global Variable -> Multicast to Soft Link
			s.linksMu.RLock()
			targetID, exists := s.links[client]
			s.linksMu.RUnlock()

			if exists && targetID != "" {
				targets := s.Get_Clients(DEFAULT_ROOM, targetID)
				s.Multicast(&Common_Packet{
					Command: "gvar", // CL4 gvar naturally translates to CL2 `vm` mode `g`
					Name:    p.Var,
					Value:   p.Data,
					Origin:  s.UserObject(client),
				}, targets)
			}
		}

	case "l_p": // Linked Private
		if client.Username == nil || client.Username == "" {
			return
		}

		targets := s.Get_Clients(DEFAULT_ROOM, p.Recipient)

		switch p.Mode {
		case "0":
			// Mode 0: Linked Private Data -> Multicast to Recipient
			s.Multicast(&Common_Packet{
				Command: "linked_pmsg", // Internal cross-protocol command
				Value:   p.Data,
				Origin:  s.UserObject(client),
			}, targets)
		case "1", "2":
			// Mode 1 & 2: Private Var -> Multicast to Recipient
			s.Multicast(&Common_Packet{
				Command: "pvar", // CL4 pvar naturally translates to CL2 `vm` mode `p`
				Name:    p.Var,
				Value:   p.Data,
				Origin:  s.UserObject(client),
			}, targets)
		}

	case "disconnect", "ds":
		// Handled gracefully by the disconnect routines
	}
}

func (s *CL2) Upgrade_Dialect(c *BridgeClient, newdialect uint) {
	if newdialect > c.dialect {
		c.dialect = newdialect
	}
}
