package server

import (
	"log"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/bwmarrin/snowflake"
	"github.com/cloudlink-delta/duplex"
	"github.com/goccy/go-json"
	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func New(designation string, config *Config) *Server {
	node, err := snowflake.NewNode(1)
	if err != nil {
		panic(err)
	}

	if designation == "" {
		panic("designation required")
	}

	if config == nil {
		panic("config required")
	}

	if config.Maximum_Rooms <= 0 {
		panic("invalid maximum rooms")
	}

	if config.Maximum_Clients <= 0 {
		panic("invalid maximum clients")
	}

	if config.Address == "" {
		config.Address = ":3000"
	}

	// Create instance and bridge manager
	instance := duplex.New("bridge@" + designation)
	instance.IsBridge = true

	server := &Server{
		Close:        make(chan bool),
		Done:         make(chan bool),
		Clients:      make(Targets),
		Config:       config,
		instance:     instance,
		RoomsMap:     make(map[RoomKey]*Room),
		snowflakeGen: node,
		App: fiber.New(fiber.Config{
			JSONEncoder: json.Marshal,
			JSONDecoder: json.Unmarshal,
		}),
	}

	// Configure bridge websocket
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

func (s *Server) Copy_Clients(room RoomKey) Clients {
	s.roomsMu.RLock()
	defer s.roomsMu.RUnlock()
	if r, ok := s.RoomsMap[room]; ok {
		return slices.Collect(maps.Keys(r.Clients))
	}
	return nil
}

func (s *Server) Unicast(c *Client, p any) {
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

func (s *Server) Broadcast(room RoomKey, p any, exclude ...*Client) {
	targets := s.Copy_Clients(room)
	if len(targets) == 0 {
		return
	}

	targets = slices.DeleteFunc(targets, func(c *Client) bool {
		return slices.Contains(exclude, c)
	})

	// Verify the client hasn't been unsubscribed right before sending
	s.roomsMu.RLock()
	defer s.roomsMu.RUnlock()
	for _, client := range targets {
		if r, ok := s.RoomsMap[room]; ok && r.Clients[client] {
			s.Unicast(client, p)
		}
	}
}

func (s *Server) Multicast(room RoomKey, p any, targets Targets) {
	if len(targets) == 0 {
		return
	}

	s.roomsMu.RLock()
	defer s.roomsMu.RUnlock()
	for client := range targets {
		if r, ok := s.RoomsMap[room]; ok && r.Clients[client] {
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
	s.clientsmu.RLock()
	count := len(s.Clients)
	s.clientsmu.RUnlock()

	if count >= int(s.Config.Maximum_Clients) {
		c.WriteMessage(websocket.TextMessage, []byte("This server is currently full. Please try again later."))
		s.Respond_With_Code(c, Overloaded_Status)
		c.Close()
		return
	}

	client := &Client{
		Conn:   c,
		ID:     s.snowflakeGen.Generate(),
		UUID:   uuid.New(),
		writer: make(chan []byte, 256),
		exit:   make(chan bool, 1),
		Rooms:  make(RoomKeys, 0),
		Server: s,
	}

	s.clientsmu.Lock()
	s.Clients[client] = true
	s.clientsmu.Unlock()

	s.Subscribe(client, DEFAULT_ROOM)

	go s.ReportActiveConnections(false)

	defer s.Destroy_Client(client)
	go client.Writer()
	client.Reader()
}

func (s *Server) Destroy_Client(c *Client) {

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

	s.clientsmu.Lock()
	delete(s.Clients, c)
	s.clientsmu.Unlock()

	close(c.writer)

	s.ReportActiveConnections(false)
}

func (s *Server) DoesRoomExist(room RoomKey) bool {
	s.roomsMu.RLock()
	defer s.roomsMu.RUnlock()
	_, exists := s.RoomsMap[room]
	return exists
}

func (s *Server) Subscribe(client *Client, room RoomKey) {
	s.roomsMu.Lock()
	r, exists := s.RoomsMap[room]
	if room == DEFAULT_ROOM {
		log.Printf("%s 🚪 Joining default room", client.GiveName())
	}
	if !exists {
		log.Printf("%s 🚪 Creating room %s", client.GiveName(), room)
		r = &Room{Clients: make(Targets)}
		s.RoomsMap[room] = r
	}

	r.Clients[client] = true
	s.roomsMu.Unlock()

	client.room_mux.Lock()
	defer client.room_mux.Unlock()
	if !slices.Contains(client.Rooms, room) {
		client.Rooms = append(client.Rooms, room)
	}

	s.ReportActiveRooms(false)
}

func (s *Server) Unsubscribe(client *Client, room RoomKey) {
	s.roomsMu.Lock()
	if r, exists := s.RoomsMap[room]; exists {
		delete(r.Clients, client)
		if len(r.Clients) == 0 {
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
	s.clientsmu.RLock()
	active_connections := len(s.Clients)
	s.clientsmu.RUnlock()
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
func (s *Server) CanAllocateNRooms(c *Client, n int) bool {
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
		if len(default_room.Clients) == 1 && default_room.Clients[c] {
			return -1
		}

		// No decrement permitted otherwise
		return 0
	}()

	active_rooms := s.ReportActiveRooms(true)

	// Projected rooms must be LESS THAN OR EQUAL TO the maximum limit
	return active_rooms+n+decrement_is_only_one_in_default <= int(s.Config.Maximum_Rooms)
}
