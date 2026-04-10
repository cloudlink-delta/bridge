package server

import (
	"log"
	"strings"

	"github.com/cloudlink-delta/duplex"
	"github.com/goccy/go-json"
)

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

		// detect if this is a plain username or a username with a suffix
		parts := strings.Split(query, "@")
		log.Println("QUERY:", query)
		log.Println("Parts:", parts)

		if len(parts) == 1 {

			// Resolve as our own
			s.ResolvePeer(query, peer, packet)

		} else {

			// Unsupported query mode
			peer.WriteBlocking(&duplex.TxPacket{
				Packet: duplex.Packet{
					Opcode:   "VIOLATION",
					Listener: packet.Listener,
					TTL:      1,
				},
				Payload: "Unsupported query mode",
			})
			peer.Close()
		}

	}, "discovery", "bridge")
}

func (i *Server) ResolvePeer(username string, peer *duplex.Peer, packet *duplex.RxPacket) {

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

	// found
	response := &QueryAck{
		Online:    true,
		Username:  username,
		IsLegacy:  true, // Always true if we're the Bridge server.
		IsRelayed: true, // Always true if we're the Bridge server.
		RelayPeer: i.Self,
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
