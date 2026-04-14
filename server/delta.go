package server

import (
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/cloudlink-delta/duplex"
	"github.com/goccy/go-json"
)

var queryRegex = regexp.MustCompile(`^([^@]+)(?:@(.+))?$`)

func (s *Server) ConfigureDelta(designation string) {
	i := s.instance

	// Configure instance callbacks
	i.OnCreate = func() {

		// Attempt to connect to the discovery server
		log.Printf("Attempting to connect to discovery server (discovery@%s)...", designation)
		i.Connect("discovery@" + designation)

	}

	i.OnDiscoveryConnected = func(peer *duplex.Peer) {

		// Cache the entry
		s.deltaclientsmu.Lock()
		defer s.deltaclientsmu.Unlock()
		s.DeltaResolverCache[peer] = HelloArgs{
			Name:        "discovery",
			Designation: designation,
		}

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
		bc := New_Delta(s).(*CLDelta).ToBridgeClient(peer)
		currentRooms := s.getDeltaRooms(peer)
		for _, room := range currentRooms {
			s.Unsubscribe(bc, room)
		}

		// Unregister cache entry if present
		s.deltaclientsmu.Lock()
		defer s.deltaclientsmu.Unlock()
		delete(s.DeltaResolverCache, peer)

		bc.Protocol.On_Disconnect(bc, currentRooms)
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

	i.Bind("HELLO", func(peer *duplex.Peer, packet *duplex.RxPacket) {
		var args HelloArgs
		if err := json.Unmarshal(packet.Payload, &args); err != nil {
			log.Printf("WARN: peer %s malformed HELLO: %v", peer.GetPeerID(), err)
			return
		}

		peer.GiveNameRemapper = func() string {
			return New_Delta(s).(*CLDelta).ToBridgeClient(peer).GiveName()
		}

		// Cache the entry
		s.deltaclientsmu.Lock()
		defer s.deltaclientsmu.Unlock()
		s.DeltaResolverCache[peer] = args
	})

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
		targetDesignation := matches[2]

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

		// Resolve as our own
		s.Resolve_Peer(username, peer, packet)

	}, "discovery", "bridge")

	i.Bind("LINK", func(peer *duplex.Peer, packet *duplex.RxPacket) {
		rooms := parseRoomsFromPayload(packet.Payload)
		bc := New_Delta(s).(*CLDelta).ToBridgeClient(peer)
		for _, room := range rooms {
			s.Subscribe(bc, room)
		}
		peer.Write(&duplex.TxPacket{
			Packet: duplex.Packet{
				Opcode:   "LINK_ACK",
				Listener: packet.Listener,
				TTL:      1,
			},
		})
		for _, room := range rooms {
			s.Sync_Room_State(bc, room)
			s.Broadcast(room, &Common_Packet{
				Command: "ulist",
				Mode:    "add",
				Value:   s.UserObject(bc),
				Rooms:   room,
			}, bc)
			s.Unicast(bc, &Common_Packet{
				Command: "ulist",
				Mode:    "set",
				Value:   s.Get_User_List(room),
				Rooms:   room,
			})
		}
	})

	i.Bind("UNLINK", func(peer *duplex.Peer, packet *duplex.RxPacket) {
		rooms := parseRoomsFromPayload(packet.Payload)
		bc := New_Delta(s).(*CLDelta).ToBridgeClient(peer)

		if len(rooms) == 0 {
			// A blank or empty array payload for UNLINK should remove all subscriptions
			rooms = s.getDeltaRooms(peer)
		}

		for _, room := range rooms {
			s.Unsubscribe(bc, room)
			s.Broadcast(room, &Common_Packet{
				Command: "ulist",
				Mode:    "set",
				Value:   s.UserObject(bc),
				Rooms:   room,
			})
		}

		peer.Write(&duplex.TxPacket{
			Packet: duplex.Packet{
				Opcode:   "UNLINK_ACK",
				Listener: packet.Listener,
				TTL:      1,
			},
		})
	})

	i.Remap("G_MSG", func(peer *duplex.Peer, packet *duplex.RxPacket) {
		bc := New_Delta(s).(*CLDelta).ToBridgeClient(peer)
		p := &Common_Packet{
			Command: "gmsg",
			Value:   packet.Payload,
			Origin:  s.PeerUserObject(peer),
		}
		rooms := s.getDeltaRooms(peer)
		if len(rooms) == 0 {
			rooms = []RoomKey{DEFAULT_ROOM}
		}
		for _, room := range rooms {
			p.Rooms = room
			s.Broadcast(room, p, bc)
		}
	})

	i.Remap("P_MSG", func(peer *duplex.Peer, packet *duplex.RxPacket) {
		p := &Common_Packet{
			Command: "pmsg",
			Value:   packet.Payload,
			Origin:  s.PeerUserObject(peer),
		}
		rooms := s.getDeltaRooms(peer)
		if len(rooms) == 0 {
			rooms = []RoomKey{DEFAULT_ROOM}
		}
		for _, room := range rooms {
			targets := s.Get_Client(packet.Target, room)
			s.Multicast(room, p, targets)
		}
	})

	i.Remap("G_VAR", func(peer *duplex.Peer, packet *duplex.RxPacket) {
		bc := New_Delta(s).(*CLDelta).ToBridgeClient(peer)
		p := &Common_Packet{
			Command: "gvar",
			Name:    packet.Id,
			Value:   packet.Payload,
			Origin:  s.PeerUserObject(peer),
		}
		rooms := s.getDeltaRooms(peer)
		if len(rooms) == 0 {
			rooms = []RoomKey{DEFAULT_ROOM}
		}
		for _, room := range rooms {
			p.Rooms = room
			s.Broadcast(room, p, bc)

			s.roomsMu.RLock()
			r, exists := s.RoomsMap[room]
			s.roomsMu.RUnlock()
			if exists {
				r.GlobalVars.Store(packet.Id, packet.Payload)
			}
		}
	})

	i.Remap("P_VAR", func(peer *duplex.Peer, packet *duplex.RxPacket) {
		p := &Common_Packet{
			Command: "pvar",
			Name:    packet.Id,
			Value:   packet.Payload,
			Origin:  s.PeerUserObject(peer),
		}
		rooms := s.getDeltaRooms(peer)
		if len(rooms) == 0 {
			rooms = []RoomKey{DEFAULT_ROOM}
		}
		for _, room := range rooms {
			targets := s.Get_Client(packet.Target, room)
			s.Multicast(room, p, targets)
		}
	})
}

func New_Delta(parent *Server) Protocol {
	return &CLDelta{
		Server: parent,
	}
}

func (d *CLDelta) ToBridgeClient(peer *duplex.Peer) *BridgeClient {
	peer.KeyLock.Lock()
	if peer.KeyStore == nil {
		peer.KeyStore = make(map[string]any)
	}
	if bc, ok := peer.KeyStore["bridge_client"].(*BridgeClient); ok {
		peer.KeyLock.Unlock()
		return bc
	}

	var sfID string
	if id, ok := peer.KeyStore["snowflake_id"].(string); ok {
		sfID = id
	} else {
		sfID = d.Server.snowflakeGen.Generate().String()
		peer.KeyStore["snowflake_id"] = sfID
	}

	bc := &BridgeClient{
		Conn:     nil, // Intentionally nil so internal Unicasts bypass standard writers
		Peer:     peer,
		ID:       sfID,
		UUID:     peer.GetPeerID(),
		writer:   make(chan []byte, 256),
		exit:     make(chan bool, 1),
		Rooms:    make(RoomKeys, 0),
		Server:   d.Server,
		Protocol: d,
	}

	peer.KeyStore["bridge_client"] = bc
	peer.KeyLock.Unlock()

	d.deltaclientsmu.RLock()
	if args, ok := d.DeltaResolverCache[peer]; ok {
		bc.Username = args.Name
	}
	d.deltaclientsmu.RUnlock()

	return bc
}

func (d *CLDelta) On_Disconnect(c *BridgeClient, rooms RoomKeys) {
	if c.Username == nil || c.Username == "" {
		return
	}

	userObj := d.UserObject(c)

	for _, room := range rooms { // <--- Loop over `rooms` param
		d.Broadcast(room, &Common_Packet{
			Command: "ulist",
			Mode:    "remove",
			Value:   userObj,
			Rooms:   room,
		}, c)
	}
}

func (d *CLDelta) Reader(client *BridgeClient, data []byte) bool {
	return false
}

func (d *CLDelta) Handler(client *BridgeClient, p any) {}

func (d *CLDelta) Upgrade_Dialect(*BridgeClient, uint) {}

// Resolve_Peer is a local resolver that finds clients for Discovery servers.
func (i *Server) Resolve_Peer(username string, peer *duplex.Peer, packet *duplex.RxPacket) {

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
		instanceID = client.UUID
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

// Get_Client is a variant of Resolve_Peer for internal use.
func (i *Server) Get_Client(query string, room RoomKey) Targets {

	if query == "*" {
		clients := i.Copy_Clients(room)
		targets := make(Targets)
		for _, c := range clients {
			targets[c] = true
		}
		return targets
	}

	matches := queryRegex.FindStringSubmatch(query)
	if matches == nil {
		return make(Targets)
	}

	username := matches[1]
	targetDesignation := matches[2]

	designation := strings.Split(i.Self, "@")[1]
	if targetDesignation != "" && targetDesignation != designation {
		return make(Targets)
	}

	log.Printf("Locally resolving classic client %s in %s", username, room)

	if username == i.Self {
		log.Printf("Local resolve classic client %s in %s returns no match (prefetch)", username, room)
		return make(Targets)
	}

	// Obtain lock
	i.classicclientsmu.Lock()
	defer i.classicclientsmu.Unlock()

	// Find
	targets := i.Get_Clients(room, username)

	log.Printf("Local resolve classic client %s in %s returns %d instances", username, room, len(targets))
	return targets
}

func (s *Server) PeerUserObject(peer *duplex.Peer) *CL4_UserObject {
	peer.KeyLock.Lock()
	if peer.KeyStore == nil {
		peer.KeyStore = make(map[string]any)
	}
	var sfID string
	if id, ok := peer.KeyStore["snowflake_id"].(string); ok {
		sfID = id
	} else {
		sfID = s.snowflakeGen.Generate().String()
		peer.KeyStore["snowflake_id"] = sfID
	}
	peer.KeyLock.Unlock()

	s.deltaclientsmu.RLock()
	defer s.deltaclientsmu.RUnlock()
	if args, ok := s.DeltaResolverCache[peer]; ok {
		return &CL4_UserObject{
			ID:       sfID,
			UUID:     peer.GetPeerID(),
			Username: args.Name,
		}
	}

	return &CL4_UserObject{
		ID:   sfID,
		UUID: peer.GetPeerID(),
	}
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
	bc := New_Delta(s).(*CLDelta).ToBridgeClient(peer)
	bc.room_mux.RLock()
	defer bc.room_mux.RUnlock()
	rooms := make([]RoomKey, len(bc.Rooms))
	copy(rooms, bc.Rooms)
	return rooms
}
