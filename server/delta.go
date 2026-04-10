package server

import (
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/cloudlink-delta/duplex"
	"github.com/goccy/go-json"
)

var queryRegex = regexp.MustCompile(`^([^.@]+)(?:\.([^@]+))?(?:@(.+))?$`)

func (s *Server) ConfigureDelta(designation string) {
	i := s.instance

	// Configure instance callbacks
	i.OnCreate = func() {

		// Attempt to connect to the discovery server
		log.Printf("Attempting to connect to discovery server (discovery@%s)...", designation)
		i.Connect("discovery@" + designation)

	}

	i.OnDiscoveryConnected = func(peer *duplex.Peer) {

		log.Printf("Discovery services connected as %s", peer.GetPeerID())
		reply := peer.WaitForMatchedPacket("AUTO_REGISTER", "VIOLATION")
		switch reply.Opcode {
		case "AUTO_REGISTER":
			log.Printf("Automatically registered on %s successfully!", peer.GetPeerID())
		case "VIOLATION":
			// The protocol mandates that VIOLATION messages have a string payload. This should never panic unless something's very wrong.
			var message string
			if err := json.Unmarshal(reply.Payload, &message); err != nil {
				panic(err)
			}
			log.Printf("Failed to auto-register on %s: %v", peer.GetPeerID(), message)
		}

	}

	// Stubs
	i.AfterNegotiation = func(peer *duplex.Peer) {}
	i.OnOpen = func(peer *duplex.Peer) {}
	i.OnClose = func(peer *duplex.Peer) {

		// Unlink from all rooms
		peer.Lock.Lock()
		var currentRooms []RoomKey
		if peer.KeyStore != nil {
			roomsSlice, _ := peer.KeyStore["classic_rooms"].([]RoomKey)
			currentRooms = make([]RoomKey, len(roomsSlice))
			copy(currentRooms, roomsSlice)
		}
		peer.Lock.Unlock()
		for _, room := range currentRooms {
			s.UnsubscribeDelta(peer, room)
		}

		// Unregister cache entry if present
		s.deltaclientsmu.Lock()
		defer s.deltaclientsmu.Unlock()
		delete(s.DeltaResolverCache, peer)
	}

	i.OnBridgeConnected = func(peer *duplex.Peer) {}
	i.OnRelayConnected = func(peer *duplex.Peer) {}

	// Stub handler that automatically declines incoming call requests
	i.Remap("CALL", func(peer *duplex.Peer, _ *duplex.RxPacket) {
		peer.Write(&duplex.TxPacket{
			Packet: duplex.Packet{
				Opcode: "DECLINE",
				TTL:    1,
			},
		})
	})

	// Translator for the classic "gmsg" command.
	i.Remap("G_MSG", func(peer *duplex.Peer, packet *duplex.RxPacket) {
		rooms := s.getDeltaRooms(peer)
		uniqueClients := make(map[*ClassicClient]bool)

		for _, room := range rooms {
			targets := s.Get_Client(packet.Target, room)
			for _, client := range targets {
				uniqueClients[client] = true
			}
		}

		for client := range uniqueClients {
			s.Unicast(client, &CL4_or_CL3_Packet{
				Command: "gmsg",
				Value:   packet.Payload,
				Origin:  s.PeerUserObject(peer),
			})
		}
	})

	// Translator for the classic "pmsg" command.
	i.Remap("P_MSG", func(peer *duplex.Peer, packet *duplex.RxPacket) {
		rooms := s.getDeltaRooms(peer)
		uniqueClients := make(map[*ClassicClient]bool)

		for _, room := range rooms {
			targets := s.Get_Client(packet.Target, room)
			for _, client := range targets {
				uniqueClients[client] = true
			}
		}

		for client := range uniqueClients {
			s.Unicast(client, &CL4_or_CL3_Packet{
				Command: "pmsg",
				Value:   packet.Payload,
				Origin:  s.PeerUserObject(peer),
			})
		}
	})

	// Translator for the classic "gvar" command.
	i.Remap("G_VAR", func(peer *duplex.Peer, packet *duplex.RxPacket) {
		rooms := s.getDeltaRooms(peer)
		uniqueClients := make(map[*ClassicClient]bool)

		s.roomsMu.RLock()
		for _, room := range rooms {
			if r, exists := s.RoomsMap[room]; exists {
				r.GlobalVars.Store(packet.Id, packet.Payload)
			}
		}
		s.roomsMu.RUnlock()

		for _, room := range rooms {
			targets := s.Get_Client(packet.Target, room)
			for _, client := range targets {
				uniqueClients[client] = true
			}
		}

		for client := range uniqueClients {
			s.Unicast(client, &CL4_or_CL3_Packet{
				Command: "gvar",
				Name:    packet.Id,
				Value:   packet.Payload,
				Origin:  s.PeerUserObject(peer),
			})
		}
	})

	// Translator for the classic "pvar" command.
	i.Remap("P_VAR", func(peer *duplex.Peer, packet *duplex.RxPacket) {
		rooms := s.getDeltaRooms(peer)
		uniqueClients := make(map[*ClassicClient]bool)

		for _, room := range rooms {
			targets := s.Get_Client(packet.Target, room)
			for _, client := range targets {
				uniqueClients[client] = true
			}
		}

		for client := range uniqueClients {
			s.Unicast(client, &CL4_or_CL3_Packet{
				Command: "pvar",
				Name:    packet.Id,
				Value:   packet.Payload,
				Origin:  s.PeerUserObject(peer),
			})
		}
	})

	// HELLO stores the peer's preferred name information in the local resolver cache.
	i.Bind("HELLO", func(peer *duplex.Peer, packet *duplex.RxPacket) {
		var args HelloArgs
		if err := json.Unmarshal(packet.Payload, &args); err != nil {
			log.Printf("WARN: peer %s malformed HELLO: %v", peer.GetPeerID(), err)
			return
		}

		// Cache the entry
		s.deltaclientsmu.Lock()
		defer s.deltaclientsmu.Unlock()
		s.DeltaResolverCache[peer] = args
	})

	// Bridge-specific handlers for Discovery service to use
	i.Bind("QUERY", func(peer *duplex.Peer, packet *duplex.RxPacket) {

		// Read the payload as a query argument
		var query string
		if err := json.Unmarshal(packet.Payload, &query); err != nil {
			peer.WriteBlocking(&duplex.TxPacket{
				Packet: duplex.Packet{
					Opcode:   "VIOLATION",
					Listener: packet.Listener,
					TTL:      1,
				},
				Payload: err.Error(),
			})
			peer.Close()
			return
		}

		log.Println("QUERY:", query)

		matches := queryRegex.FindStringSubmatch(query)
		if matches == nil {
			peer.WriteBlocking(&duplex.TxPacket{
				Packet: duplex.Packet{
					Opcode:   "VIOLATION",
					Listener: packet.Listener,
					TTL:      1,
				},
				Payload: "Invalid query format",
			})
			peer.Close()
			return
		}

		username := matches[1]
		bridgeName := matches[2]
		targetDesignation := matches[3]

		if targetDesignation != "" && targetDesignation != designation {
			peer.WriteBlocking(&duplex.TxPacket{
				Packet: duplex.Packet{
					Opcode:   "VIOLATION",
					Listener: packet.Listener,
					TTL:      1,
				},
				Payload: "Unsupported query mode: designation mismatch",
			})
			peer.Close()
			return
		}

		if bridgeName != "" {
			expectedBridge := strings.Split(s.Self, "@")[0]
			if bridgeName != expectedBridge && bridgeName != s.Self {
				// Not meant for us
				peer.Write(&duplex.TxPacket{
					Packet: duplex.Packet{
						Opcode:   "QUERY_ACK",
						Listener: packet.Listener,
						TTL:      1,
					},
					Payload: QueryAck{
						Username: query,
						Online:   false,
					},
				})
				return
			}
		}

		// Resolve as our own
		s.ResolvePeer(username, peer, packet)

	}, "discovery", "bridge")

	// LINK is a compatibility command that joins a Delta client to a classic room(s).
	i.Bind("LINK", func(peer *duplex.Peer, packet *duplex.RxPacket) {
		rooms := parseRoomsFromPayload(packet.Payload)
		for _, room := range rooms {
			s.SubscribeDelta(peer, room)
		}
		peer.Write(&duplex.TxPacket{
			Packet: duplex.Packet{
				Opcode:   "LINK_ACK",
				Listener: packet.Listener,
				TTL:      1,
			},
		})
	})

	// UNLINK is a compatibility command that removes a Delta client from a classic room(s).
	i.Bind("UNLINK", func(peer *duplex.Peer, packet *duplex.RxPacket) {
		rooms := parseRoomsFromPayload(packet.Payload)
		if len(rooms) == 0 {
			// A blank or empty array payload for UNLINK should remove all subscriptions
			peer.Lock.Lock()
			var currentRooms []RoomKey
			if peer.KeyStore != nil {
				roomsSlice, _ := peer.KeyStore["classic_rooms"].([]RoomKey)
				currentRooms = make([]RoomKey, len(roomsSlice))
				copy(currentRooms, roomsSlice)
			}
			peer.Lock.Unlock()
			for _, room := range currentRooms {
				s.UnsubscribeDelta(peer, room)
			}
		} else {
			for _, room := range rooms {
				s.UnsubscribeDelta(peer, room)
			}
		}
		peer.Write(&duplex.TxPacket{
			Packet: duplex.Packet{
				Opcode:   "UNLINK_ACK",
				Listener: packet.Listener,
				TTL:      1,
			},
		})
	})
}

func parseRoomsFromPayload(payload json.RawMessage) []RoomKey {
	if len(payload) == 0 || string(payload) == "null" {
		return nil
	}
	var array []any
	if err := json.Unmarshal(payload, &array); err == nil {
		var rooms []RoomKey
		for _, v := range array {
			if v != nil && fmt.Sprintf("%v", v) != "" {
				rooms = append(rooms, RoomKey(fmt.Sprintf("%v", v)))
			}
		}
		return rooms
	}
	var single any
	if err := json.Unmarshal(payload, &single); err == nil {
		str := fmt.Sprintf("%v", single)
		if single == nil || str == "" {
			return nil
		}
		return []RoomKey{RoomKey(str)}
	}
	return nil
}

func (s *Server) getDeltaRooms(peer *duplex.Peer) []RoomKey {
	peer.Lock.Lock()
	defer peer.Lock.Unlock()
	if peer.KeyStore == nil {
		return nil
	}
	roomsSlice, _ := peer.KeyStore["classic_rooms"].([]RoomKey)
	rooms := make([]RoomKey, len(roomsSlice))
	copy(rooms, roomsSlice)
	return rooms
}

func (i *Server) ResolvePeer(username string, peer *duplex.Peer, packet *duplex.RxPacket) {

	designation := strings.Split(i.Self, "@")[1]
	expectedBridge := strings.Split(i.Self, "@")[0]

	// 1. Check if the query is asking for the bridge server itself
	if username == expectedBridge || username == i.Self {
		peer.Write(&duplex.TxPacket{
			Packet: duplex.Packet{
				Opcode:   "QUERY_ACK",
				Listener: packet.Listener,
				TTL:      1,
			},
			Payload: QueryAck{
				Online:      true,
				Username:    username,
				Designation: designation,
				InstanceID:  i.Self,
				IsBridge:    true,
			},
		})
		return
	}

	// Obtain lock
	i.classicclientsmu.Lock()
	defer i.classicclientsmu.Unlock()

	// Find
	targets := i.Get_Clients(DEFAULT_ROOM, username)

	// not found
	if len(targets) == 0 {
		peer.Write(&duplex.TxPacket{
			Packet: duplex.Packet{
				Opcode:   "QUERY_ACK",
				Listener: packet.Listener,
				TTL:      1,
			},
			Payload: QueryAck{
				Username: username,
				Online:   false,
			},
		})
		return
	}

	if len(targets) > 1 {
		log.Printf("WARN: resolver found more than 1 client for a single query: %s", username)
	}

	var instanceID string
	for client := range targets {
		instanceID = client.UUID.String()
		break
	}

	// found
	response := &QueryAck{
		Online:      true,
		Username:    username, // {username}.bridge@{designation}
		Designation: designation,
		InstanceID:  instanceID,
		IsLegacy:    true, // Always true if we're the Bridge server.
		IsRelayed:   true, // Always true if we're the Bridge server.
		RelayPeer:   i.Self,
	}

	peer.Write(&duplex.TxPacket{
		Packet: duplex.Packet{
			Opcode:   "QUERY_ACK",
			Listener: packet.Listener,
			TTL:      1,
		},
		Payload: response,
	})
}

func (i *Server) Get_Client(query string, room RoomKey) []*ClassicClient {

	if query == "*" {
		return i.Copy_Clients(room)
	}

	matches := queryRegex.FindStringSubmatch(query)
	if matches == nil {
		return make([]*ClassicClient, 0)
	}

	username := matches[1]
	bridgeName := matches[2]
	targetDesignation := matches[3]

	designation := strings.Split(i.Self, "@")[1]
	if targetDesignation != "" && targetDesignation != designation {
		return make([]*ClassicClient, 0)
	}

	log.Printf("Locally resolving classic client %s in %s", username, room)

	if username == bridgeName || username == i.Self {
		log.Printf("Local resolve classic client %s in %s returns no match (prefetch)", username, room)
		return make([]*ClassicClient, 0)
	}

	// Obtain lock
	i.classicclientsmu.Lock()
	defer i.classicclientsmu.Unlock()

	// Find
	targets := i.Get_Clients(room, username)

	var instances []*ClassicClient
	for client := range targets {
		instances = append(instances, client)
	}

	log.Printf("Local resolve classic client %s in %s returns %d instances", username, room, len(instances))
	return instances
}

func (s *Server) PeerUserObject(peer *duplex.Peer) *CL4_UserObject {

	s.deltaclientsmu.RLock()
	defer s.deltaclientsmu.RUnlock()
	if args, ok := s.DeltaResolverCache[peer]; ok {
		return &CL4_UserObject{
			ID:       peer.GetPeerID(),
			UUID:     peer.GetPeerID(),
			Username: args.Name,
		}
	}

	return &CL4_UserObject{
		ID:   peer.GetPeerID(),
		UUID: peer.GetPeerID(),
	}
}
