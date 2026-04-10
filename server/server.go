package server

import (
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/snowflake"
	"github.com/cloudlink-delta/duplex"
	"github.com/goccy/go-json"
	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func New(designation string, server_config *Config, duplex_config *duplex.Config) *Server {
	node, err := snowflake.NewNode(1)
	if err != nil {
		panic(err)
	}

	if designation == "" {
		panic("designation required")
	}

	if server_config == nil {
		panic("config required")
	}

	if server_config.Maximum_Rooms <= 0 {
		panic("invalid maximum rooms")
	}

	if server_config.Maximum_Clients <= 0 {
		panic("invalid maximum clients")
	}

	if server_config.Address == "" {
		server_config.Address = ":3000"
	}

	self := "bridge@" + designation

	// Create instance and bridge manager
	instance := duplex.New(self, duplex_config)
	instance.IsBridge = true

	server := &Server{
		Self:               self,
		Close:              make(chan bool),
		Done:               make(chan bool),
		DeltaClients:       make(DeltaTargets),
		ClassicClients:     make(ClassicTargets),
		DeltaResolverCache: make(map[*duplex.Peer]HelloArgs),
		Config:             server_config,
		instance:           instance,
		RoomsMap:           make(map[RoomKey]*Room),
		snowflakeGen:       node,
		App: fiber.New(fiber.Config{
			JSONEncoder: json.Marshal,
			JSONDecoder: json.Unmarshal,
		}),
	}

	// Configure CL2 / CL3 / CL4 / Scratch CloudVars Gateway
	server.App.Use("*", func(c *fiber.Ctx) error {
		if websocket.IsWebSocketUpgrade(c) {
			c.Locals("allowed", true)
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})

	server.App.Get("*", websocket.New(func(c *websocket.Conn) {
		server.Run_Client(c)
	}))

	// Configure Delta Peer
	server.ConfigureDelta(designation)

	return server
}

func (s *Server) Run() {
	// Init waitgroup
	var wg sync.WaitGroup
	wg.Add(2) // Add 2 waitgroup tasks

	// Launch fiber app
	go func() {
		defer wg.Done()
		if err := s.App.Listen(s.Config.Address); err != nil {
			log.Printf("Fiber app error: %v", err)
		}
	}()

	// Launch instance app
	go func() {
		defer wg.Done()
		s.instance.Run()
	}()

	// Wait for close signal
	<-s.Close

	// Shutdown components
	_ = s.App.Shutdown()
	s.instance.Close <- true
	<-s.instance.Done

	wg.Wait() // Wait for both apps to finish
	s.Done <- true
}

func (s *Server) Copy_Clients(room RoomKey) ClassicClients {
	s.roomsMu.RLock()
	defer s.roomsMu.RUnlock()
	if r, ok := s.RoomsMap[room]; ok {
		return slices.Collect(maps.Keys(r.ClassicClients))
	}
	return nil
}

func (s *Server) Unicast(c *ClassicClient, p any) {
	patched := c.Protocol.Apply_Quirks(c, p)
	if patched == nil {
		return
	}

	msg, err := json.Marshal(patched)
	if err != nil {
		log.Fatalf("Failed to marshal packet: %v", err)
	}

	if c.Conn == nil {
		return
	}

	select {
	case c.writer <- msg:
	default:
		log.Printf("Warning: Client %s buffer full, dropping message", c.ID)
	}
}

func (s *Server) Broadcast(room RoomKey, p any, exclude ...*ClassicClient) {
	targets := s.Copy_Clients(room)
	if len(targets) == 0 {
		return
	}

	targets = slices.DeleteFunc(targets, func(c *ClassicClient) bool {
		return slices.Contains(exclude, c)
	})

	// Verify the client hasn't been unsubscribed right before sending
	s.roomsMu.RLock()
	defer s.roomsMu.RUnlock()
	for _, client := range targets {
		if r, ok := s.RoomsMap[room]; ok && r.ClassicClients[client] {
			s.Unicast(client, p)
		}
	}
}

func (s *Server) Multicast(room RoomKey, p any, targets ClassicTargets) {
	if len(targets) == 0 {
		return
	}

	s.roomsMu.RLock()
	defer s.roomsMu.RUnlock()
	for client := range targets {
		if r, ok := s.RoomsMap[room]; ok && r.ClassicClients[client] {
			s.Unicast(client, p)
		}
	}
}

func (s *Server) Run_Client(c *websocket.Conn) {

	// Abort the connection if we detect a session token for no reason.
	if c.Cookies("scratchsessionsid", "") != "" {
		c.WriteMessage(websocket.TextMessage, []byte(
			"The cloud data library you are using is putting "+
				"your Scratch account at risk by sending us your "+
				"login token for no reason. Change your Scratch "+
				"password immediately, then contact the maintainers "+
				"of that library for further information. This "+
				"connection is being refused to protect your security."))
		s.Respond_With_Code(c, Security_Error)
		c.Close()
		return
	}

	// Abort connection if the server is overloaded
	s.classicclientsmu.RLock()
	count := len(s.ClassicClients)
	s.classicclientsmu.RUnlock()

	if count >= int(s.Config.Maximum_Clients) {
		c.WriteMessage(websocket.TextMessage, []byte("This server is currently full. Please try again later."))
		s.Respond_With_Code(c, Overloaded_Status)
		c.Close()
		return
	}

	client := &ClassicClient{
		Conn:   c,
		ID:     s.snowflakeGen.Generate(),
		UUID:   uuid.New(),
		writer: make(chan []byte, 256),
		exit:   make(chan bool, 1),
		Rooms:  make(RoomKeys, 0),
		Server: s,
	}

	s.classicclientsmu.Lock()
	s.ClassicClients[client] = true
	s.classicclientsmu.Unlock()

	s.Subscribe(client, DEFAULT_ROOM)

	go s.ReportActiveConnections(false)

	defer s.Destroy_Client(client)
	go client.Writer()
	client.Reader()
}

func (s *Server) Destroy_Client(c *ClassicClient) {

	// Safely copy the rooms slice so we don't mutate it while iterating
	c.room_mux.RLock()
	roomsToLeave := make(RoomKeys, len(c.Rooms))
	copy(roomsToLeave, c.Rooms)
	c.room_mux.RUnlock()

	// 1. Fully purge the client from the server's room states BEFORE announcing
	for _, room := range roomsToLeave {
		s.Unsubscribe(c, room)
	}

	// 2. If the default room somehow failed to be removed, attempt to remove it.
	if s.Is_Client_In_Room(c, DEFAULT_ROOM) {
		s.Unsubscribe(c, DEFAULT_ROOM)
	}

	// 3. NOW fire the protocol disconnect handlers.
	// Because the client is purged, any Get_User_List calls will correctly exclude them!
	if c.Protocol != nil {
		c.Protocol.On_Disconnect(c, roomsToLeave)
	}

	s.AnnounceClassicLeft(c, roomsToLeave)

	s.classicclientsmu.Lock()
	delete(s.ClassicClients, c)
	s.classicclientsmu.Unlock()

	close(c.writer)

	s.ReportActiveConnections(false)
}

func (s *Server) DoesRoomExist(room RoomKey) bool {
	s.roomsMu.RLock()
	defer s.roomsMu.RUnlock()
	_, exists := s.RoomsMap[room]
	return exists
}

func (s *Server) Subscribe(client *ClassicClient, room RoomKey) {
	s.roomsMu.Lock()
	r, exists := s.RoomsMap[room]
	if room == DEFAULT_ROOM {
		log.Printf("%s 🚪 Joining default room", client.GiveName())
	}
	if !exists {
		log.Printf("%s 🚪 Creating room %s", client.GiveName(), room)
		r = &Room{ClassicClients: make(ClassicTargets)}
		s.RoomsMap[room] = r
	}

	r.ClassicClients[client] = true
	s.roomsMu.Unlock()

	client.room_mux.Lock()
	defer client.room_mux.Unlock()
	if !slices.Contains(client.Rooms, room) {
		client.Rooms = append(client.Rooms, room)
	}

	s.ReportActiveRooms(false)
}

func (s *Server) Unsubscribe(client *ClassicClient, room RoomKey) {
	s.roomsMu.Lock()
	if r, exists := s.RoomsMap[room]; exists {
		delete(r.ClassicClients, client)
		if len(r.ClassicClients) == 0 {
			delete(s.RoomsMap, room)
			log.Printf("%s 🚪 Destroying vacant room %s", client.GiveName(), room)
		}
	}
	s.roomsMu.Unlock()

	client.room_mux.Lock()
	defer client.room_mux.Unlock()
	client.Rooms = slices.DeleteFunc(client.Rooms, func(rk RoomKey) bool {
		return rk == room
	})

	s.ReportActiveRooms(false)
}

func (*Server) Respond_With_Code(c *websocket.Conn, code SocketCodes) error {
	return c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(int(code.Code), code.Message), time.Now().Add(time.Second))
}

func (s *Server) ReportActiveRooms(silent bool) int {
	s.roomsMu.RLock()
	rooms_open := len(s.RoomsMap)
	s.roomsMu.RUnlock()
	if !silent {
		log.Printf("🚪 There are %v room(s) active.", rooms_open)
	}
	return rooms_open
}

func (s *Server) ReportActiveConnections(silent bool) int {
	s.classicclientsmu.RLock()
	active_connections := len(s.ClassicClients)
	s.classicclientsmu.RUnlock()
	if !silent {
		log.Printf("🔌 There are %v connection(s) active.", active_connections)
	}
	return active_connections
}

func (s *Server) DisplayStatus() {
	s.ReportActiveConnections(false)
	s.ReportActiveRooms(false)
}

// CanAllocateNRooms checks if the current room count + n doesn't exceed the limit.
// Decreases the projected total by 1 if the client is the only connected peer in the default room.
// This function assumes that if granted, the client joins the n allocated rooms
// and frees the default room from memory.
func (s *Server) CanAllocateNRooms(c *ClassicClient, n int) bool {
	decrement_is_only_one_in_default := func() int {
		s.roomsMu.RLock()
		default_room, ok := s.RoomsMap[DEFAULT_ROOM]
		defer s.roomsMu.RUnlock()

		// If the room doesn't exist, it isn't taking up an active room slot.
		// We cannot give a discount.
		if !ok {
			return 0
		}

		// Allow the decrement since the peer is the only one in the room,
		// joining the new room would render the default empty and eligible for GC.
		if len(default_room.ClassicClients) == 1 && default_room.ClassicClients[c] {
			return -1
		}

		// No decrement permitted otherwise
		return 0
	}()

	active_rooms := s.ReportActiveRooms(true)

	// Projected rooms must be LESS THAN OR EQUAL TO the maximum limit
	return active_rooms+n+decrement_is_only_one_in_default <= int(s.Config.Maximum_Rooms)
}

func (s *Server) SubscribeDelta(peer *duplex.Peer, room RoomKey) {
	s.roomsMu.Lock()
	r, exists := s.RoomsMap[room]
	if !exists {
		r = &Room{ClassicClients: make(ClassicTargets), DeltaClients: make(DeltaTargets)}
		s.RoomsMap[room] = r
	}
	if r.DeltaClients == nil {
		r.DeltaClients = make(DeltaTargets)
	}
	r.DeltaClients[peer] = true
	s.roomsMu.Unlock()

	peer.Lock.Lock()
	defer peer.Lock.Unlock()
	if peer.KeyStore == nil {
		peer.KeyStore = make(map[string]any)
	}
	rooms, _ := peer.KeyStore["classic_rooms"].([]RoomKey)
	if !slices.Contains(rooms, room) {
		peer.KeyStore["classic_rooms"] = append(rooms, room)
	}
}

func (s *Server) UnsubscribeDelta(peer *duplex.Peer, room RoomKey) {
	s.roomsMu.Lock()
	if r, exists := s.RoomsMap[room]; exists {
		if r.DeltaClients != nil {
			delete(r.DeltaClients, peer)
		}
		if len(r.ClassicClients) == 0 && len(r.DeltaClients) == 0 {
			delete(s.RoomsMap, room)
		}
	}
	s.roomsMu.Unlock()

	peer.Lock.Lock()
	defer peer.Lock.Unlock()
	if peer.KeyStore != nil {
		rooms, _ := peer.KeyStore["classic_rooms"].([]RoomKey)
		peer.KeyStore["classic_rooms"] = slices.DeleteFunc(rooms, func(rk RoomKey) bool {
			return rk == room
		})
	}
}

func (s *Server) AnnounceClassicJoin(client *ClassicClient, rooms []RoomKey) {
	if client.Username == nil || client.Username == "" {
		return
	}

	if rooms == nil {
		client.room_mux.RLock()
		rooms = make([]RoomKey, len(client.Rooms))
		copy(rooms, client.Rooms)
		client.room_mux.RUnlock()
	}

	designation := strings.Split(s.Self, "@")[1]

	payload := QueryAck{
		Online:      true,
		Username:    fmt.Sprintf("%v", client.Username),
		Designation: designation,
		InstanceID:  client.UUID.String(),
		IsLegacy:    true,
		IsRelayed:   true,
		RelayPeer:   s.Self,
	}

	uniquePeers := make(map[*duplex.Peer]bool)
	s.roomsMu.RLock()
	for _, room := range rooms {
		if r, exists := s.RoomsMap[room]; exists {
			for peer := range r.DeltaClients {
				uniquePeers[peer] = true
			}
		}
	}
	s.roomsMu.RUnlock()

	for peer := range uniquePeers {
		peer.Write(&duplex.TxPacket{
			Packet: duplex.Packet{
				Opcode: "CLASSIC_JOIN",
				TTL:    1,
			},
			Payload: payload,
		})
	}
}

func (s *Server) AnnounceClassicLeft(client *ClassicClient, roomsToLeave []RoomKey) {
	if client.Username == nil || client.Username == "" {
		return
	}

	if roomsToLeave == nil {
		client.room_mux.RLock()
		roomsToLeave = make([]RoomKey, len(client.Rooms))
		copy(roomsToLeave, client.Rooms)
		client.room_mux.RUnlock()
	}

	client.room_mux.RLock()
	remainingRooms := make([]RoomKey, len(client.Rooms))
	copy(remainingRooms, client.Rooms)
	client.room_mux.RUnlock()

	retainedPeers := make(map[*duplex.Peer]bool)
	s.roomsMu.RLock()
	for _, room := range remainingRooms {
		if r, exists := s.RoomsMap[room]; exists {
			for peer := range r.DeltaClients {
				retainedPeers[peer] = true
			}
		}
	}

	notifyPeers := make(map[*duplex.Peer]bool)
	for _, room := range roomsToLeave {
		if r, exists := s.RoomsMap[room]; exists {
			for peer := range r.DeltaClients {
				if !retainedPeers[peer] {
					notifyPeers[peer] = true
				}
			}
		}
	}
	s.roomsMu.RUnlock()

	if len(notifyPeers) == 0 {
		return
	}

	designation := strings.Split(s.Self, "@")[1]

	payload := QueryAck{
		Online:      false,
		Username:    fmt.Sprintf("%v", client.Username),
		Designation: designation,
		InstanceID:  client.UUID.String(),
		IsLegacy:    true,
		IsRelayed:   true,
		RelayPeer:   s.Self,
	}

	for peer := range notifyPeers {
		peer.Write(&duplex.TxPacket{
			Packet: duplex.Packet{
				Opcode: "CLASSIC_LEFT",
				TTL:    1,
			},
			Payload: payload,
		})
	}
}
