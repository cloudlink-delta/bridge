package server

import (
	"log"
	"slices"
	"sync"
	"time"

	"github.com/bwmarrin/snowflake"
	"github.com/cloudlink-delta/duplex"
	"github.com/goccy/go-json"
	"github.com/gofiber/contrib/monitor"
	"github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
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

	if server_config.Rate_Limit_Burst <= 0 {
		panic("invalid rate limit burst")
	}

	if server_config.Rate_Limit_Interval <= 0 {
		panic("invalid rate limit interval")
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
		ClassicClients:     make(Targets),
		DeltaResolverCache: make(map[*duplex.Peer]HelloArgs),
		Config:             server_config,
		instance:           instance,
		RoomsMap:           make(map[RoomKey]*Room),
		roomEvents:         make(chan RoomEvent, 1024),
		snowflakeGen:       node,
		App: fiber.New(fiber.Config{
			JSONEncoder:   json.Marshal,
			JSONDecoder:   json.Unmarshal,
			StrictRouting: true,
		}),
	}

	// Configure Health endpoint
	server.App.Get("/health", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status":         server.instance.GetPeerState(),
			"active_clients": server.ReportActiveConnections(true),
			"active_rooms":   server.ReportActiveRooms(),
		})
	})

	server.App.Get("/metrics", monitor.New())

	// Configure CL2 / CL3 / CL4 / Scratch CloudVars Gateway
	server.App.Use("/", func(c fiber.Ctx) error {
		if websocket.IsWebSocketUpgrade(c) {
			c.Locals("allowed", true)
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})

	server.App.Get("/", websocket.New(func(c *websocket.Conn) {
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

	// Launch Room Manager
	go s.RoomManager()

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

func (s *Server) RoomManager() {
	for event := range s.roomEvents {
		switch event.Op {
		case OpJoinRoom:
			r, exists := s.RoomsMap[event.Room]
			if !exists {
				log.Printf("%s 🚪 Creating room %s", event.Client.GiveName(), event.Room)
				r = &Room{Clients: make(Targets)}
				s.RoomsMap[event.Room] = r
			}
			r.Clients[event.Client] = true
		case OpLeaveRoom:
			if r, exists := s.RoomsMap[event.Room]; exists {
				delete(r.Clients, event.Client)
				if len(r.Clients) == 0 {
					delete(s.RoomsMap, event.Room)
					log.Printf("%s 🚪 Destroying vacant room %s", event.Client.GiveName(), event.Room)
				}
			}
		case OpGetClients:
			var clients BridgeClients
			if r, exists := s.RoomsMap[event.Room]; exists {
				clients = make(BridgeClients, 0, len(r.Clients))
				for c := range r.Clients {
					clients = append(clients, c)
				}
			}
			event.Respond <- clients
		case OpGetTargets:
			var targets Targets
			if r, exists := s.RoomsMap[event.Room]; exists {
				targets = make(Targets)
				for c := range r.Clients {
					targets[c] = true
				}
			}
			event.Respond <- targets
		case OpDoesRoomExist:
			_, exists := s.RoomsMap[event.Room]
			event.Respond <- exists
		case OpGetActiveRooms:
			event.Respond <- len(s.RoomsMap)
		case OpCanAllocateNRooms:
			active_rooms := len(s.RoomsMap)
			decrement := 0
			if default_room, ok := s.RoomsMap[DEFAULT_ROOM]; ok {
				if len(default_room.Clients) == 1 && default_room.Clients[event.Client] {
					decrement = -1
				}
			}
			event.Respond <- active_rooms+event.N+decrement <= int(s.Config.Maximum_Rooms)
		case OpGetRoomForVars:
			if r, exists := s.RoomsMap[event.Room]; exists {
				event.Respond <- &r.GlobalVars
			} else {
				event.Respond <- (*sync.Map)(nil)
			}
		}
	}
}

func (s *Server) GetRoomGlobalVars(room RoomKey) *sync.Map {
	resp := make(chan any, 1)
	s.roomEvents <- RoomEvent{Op: OpGetRoomForVars, Room: room, Respond: resp}
	return (<-resp).(*sync.Map)
}

func (s *Server) Copy_Clients(room RoomKey) BridgeClients {
	resp := make(chan any, 1)
	s.roomEvents <- RoomEvent{Op: OpGetClients, Room: room, Respond: resp}
	return (<-resp).(BridgeClients)
}

func (s *Server) Get_Targets(room RoomKey) Targets {
	resp := make(chan any, 1)
	s.roomEvents <- RoomEvent{Op: OpGetTargets, Room: room, Respond: resp}
	return (<-resp).(Targets)
}

func (c *BridgeClients) Targets() Targets {
	targets := make(Targets)
	for _, client := range *c {
		targets[client] = true
	}
	return targets
}

func (s *Server) Unicast(c *BridgeClient, p Packet) {
	if c == nil || c.Protocol == nil {
		return
	}

	// Apply translation / quirks
	patched := c.Protocol.Apply_Quirks(c, p)
	if patched == nil {
		return
	}

	// Marshal the packet
	msg, err := json.Marshal(patched)
	if err != nil {
		log.Fatalf("⚠️  Failed to marshal packet: %v", err)
	}

	// Abort if the client somehow turned nil
	if c.Conn == nil {
		return
	}

	// Check if client is still active to avoid "send on closed channel"
	s.classicclientsmu.RLock()
	active := s.ClassicClients[c]
	s.classicclientsmu.RUnlock()
	if !active {
		return
	}

	defer func() {
		if r := recover(); r != nil {
			log.Printf("⚠️  Recovered from panic: %v", r)
			// Ignore send on closed channel panics
		}
	}()

	s.safeSend(c, msg)
}

func (s *Server) Broadcast(room RoomKey, p Packet, exclude ...*BridgeClient) {
	if p == nil {
		return
	}

	targets := s.Get_Targets(room)
	if len(targets) == 0 {
		return
	}

	// Filter out targets
	for _, client := range exclude {
		delete(targets, client)
	}

	s.multicast(p, targets)
}

func (s *Server) Multicast(p Packet, targets Targets) {
	if p == nil {
		return
	}

	if len(targets) == 0 {
		return
	}

	s.multicast(p, targets)
}

func (s *Server) multicast(p Packet, targets Targets) {
	if p == nil || len(targets) == 0 {
		return
	}

	groups := make(map[groupKey][]*BridgeClient)
	for target := range targets {
		if target.Protocol == nil {
			continue
		}
		key := groupKey{target.Protocol, target.dialect}
		groups[key] = append(groups[key], target)
	}

	for key, g_targets := range groups {
		var representative *BridgeClient
		if len(g_targets) > 0 {
			representative = g_targets[0]
		}

		patched := key.proto.Apply_Quirks(representative, p)
		if patched == nil {
			continue
		}

		msg, err := json.Marshal(patched)
		if err != nil {
			log.Printf("⚠️  Failed to marshal packet: %v", err)
			continue
		}
		for _, target := range g_targets {
			if target == nil || target.Conn == nil {
				continue
			}
			s.safeSend(target, msg)
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

	client := &BridgeClient{
		Conn:   c,
		ID:     s.snowflakeGen.Generate().String(),
		UUID:   uuid.New().String(),
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

func (s *Server) Destroy_Client(c *BridgeClient) {

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

	s.classicclientsmu.Lock()
	delete(s.ClassicClients, c)
	s.classicclientsmu.Unlock()

	select {
	case c.exit <- true:
	default:
	}

	s.ReportActiveConnections(false)
}

func (s *Server) safeSend(c *BridgeClient, msg []byte) {
	defer func() {
		recover() // Ignore panics from sending to a closed channel
	}()

	select {
	case c.writer <- msg:
	default:
		// Channel full, drop packet (standard for WebSockets/Real-time)
	}
}

func (s *Server) DoesRoomExist(room RoomKey) bool {
	resp := make(chan any, 1)
	s.roomEvents <- RoomEvent{Op: OpDoesRoomExist, Room: room, Respond: resp}
	return (<-resp).(bool)
}

func (s *Server) Subscribe(client *BridgeClient, room RoomKey) {
	s.roomEvents <- RoomEvent{Op: OpJoinRoom, Client: client, Room: room}

	client.room_mux.Lock()
	defer client.room_mux.Unlock()
	if !slices.Contains(client.Rooms, room) {
		client.Rooms = append(client.Rooms, room)
	}
}

func (s *Server) Unsubscribe(client *BridgeClient, room RoomKey) {
	s.roomEvents <- RoomEvent{Op: OpLeaveRoom, Client: client, Room: room}

	client.room_mux.Lock()
	defer client.room_mux.Unlock()
	client.Rooms = slices.DeleteFunc(client.Rooms, func(rk RoomKey) bool {
		return rk == room
	})
}

func (*Server) Respond_With_Code(c *websocket.Conn, code SocketCodes) error {
	return c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(int(code.Code), code.Message), time.Now().Add(time.Second))
}

func (s *Server) ReportActiveRooms() int {
	resp := make(chan any, 1)
	s.roomEvents <- RoomEvent{Op: OpGetActiveRooms, Respond: resp}
	rooms_open := (<-resp).(int)
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

// CanAllocateNRooms checks if the current room count + n doesn't exceed the limit.
// Decreases the projected total by 1 if the client is the only connected peer in the default room.
// This function assumes that if granted, the client joins the n allocated rooms
// and frees the default room from memory.
func (s *Server) CanAllocateNRooms(c *BridgeClient, n int) bool {
	resp := make(chan any, 1)
	s.roomEvents <- RoomEvent{Op: OpCanAllocateNRooms, Client: c, N: n, Respond: resp}
	return (<-resp).(bool)
}
