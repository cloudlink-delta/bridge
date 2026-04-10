package server

import (
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
	i.OnClose = func(peer *duplex.Peer) {}
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

	// Remap handlers for the Delta protocol
	i.Remap("G_MSG", func(*duplex.Peer, *duplex.RxPacket) {})
	i.Remap("P_MSG", func(*duplex.Peer, *duplex.RxPacket) {})
	i.Remap("G_VAR", func(*duplex.Peer, *duplex.RxPacket) {})
	i.Remap("P_VAR", func(*duplex.Peer, *duplex.RxPacket) {})

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
	i.Bind("LINK", func(*duplex.Peer, *duplex.RxPacket) {})

	// UNLINK is a compatibility command that removes a Delta client from a classic room(s).
	i.Bind("UNLINK", func(*duplex.Peer, *duplex.RxPacket) {})
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
	i.clientsmu.Lock()
	defer i.clientsmu.Unlock()

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
