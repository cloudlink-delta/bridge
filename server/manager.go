package server

import (
	"log"
	"sync"

	"github.com/bwmarrin/snowflake"
	"github.com/cloudlink-delta/duplex"
	"github.com/gofiber/contrib/websocket"
	"github.com/google/uuid"
)

func New(instance *duplex.Instance) *Manager {

	// Initialize snowflake node
	node, err := snowflake.NewNode(1)
	if err != nil {
		panic(err)
	}

	// Initialize manager
	manager := &Manager{
		instance:      instance,
		node:          node,
		connections:   make(map[snowflake.ID]*Client),
		rooms:         make(map[string]*Room),
		lock:          &sync.Mutex{},
		ServerVersion: "1.2.0",
		VeryVerbose:   true,
		Config: &Config{
			EnableMOTD:       false,
			MOTDMessage:      "",
			ServeIPAddresses: false,
		},
	}

	// Create default room
	manager.DefaultRoom = manager.CreateRoom("default")

	return manager
}

func (m *Manager) Create(conn *websocket.Conn) *Client {
	c := &Client{
		ID:       m.node.Generate(),
		UUID:     uuid.New(),
		Rooms:    make(map[any]*Room),
		conn:     conn,
		exit:     make(chan bool),
		reader:   make(chan any),
		writer:   make(chan any),
		tx:       &sync.Mutex{},
		roomlock: &sync.Mutex{},
		manager:  m,
		dialect:  Dialect_Undefined,
	}

	m.lock.Lock()
	defer m.lock.Unlock()
	func() {
		m.connections[c.ID] = c
	}()

	log.Printf("%s %v", c.GiveName(), "Connection created")
	return c
}

func (m *Manager) Destroy(c *Client) {
	rooms := c.AllRooms()
	for _, room := range rooms {
		c.LeaveRoom(room)
	}

	id := c.ID

	m.lock.Lock()
	defer m.lock.Unlock()
	func() {
		delete(m.connections, id)
	}()

	close(c.exit)

	log.Printf("%s %v", c.GiveName(), "Connection destroyed")
}
