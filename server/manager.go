package cloudlink

import (
	"fmt"
	"log"
	"sync"

	"github.com/bwmarrin/snowflake"
	"github.com/cloudlink-delta/duplex"
	"github.com/gofiber/contrib/websocket"
	"github.com/google/uuid"
)

type Manager struct {
	instance      *duplex.Instance
	node          *snowflake.Node
	connections   map[snowflake.ID]*Client
	rooms         map[string]*Room
	lock          *sync.Mutex
	DefaultRoom   *Room
	ServerVersion string
	VeryVerbose   bool
	Config        *Config
}

type Config struct {
	EnableMOTD       bool
	MOTDMessage      string
	ServeIPAddresses bool
}

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
		protocol: Protocol_Undefined,
		dialect:  Dialect_Undefined,
		conn:     conn,
		exit:     make(chan bool),
		reader:   make(chan any),
		writer:   make(chan any),
		tx:       &sync.Mutex{},
		roomlock: &sync.Mutex{},
		manager:  m,
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

func (m *Manager) Unicast(client *Client, packet any) {
	client.writer <- packet
}

func (m *Manager) BroadcastToRoom(room *Room, packet any, exclusions ...*Client) {
	m.Multicast(room.Clients, packet, exclusions...)
}

func (m *Manager) Multicast(clients map[snowflake.ID]*Client, packet any, exclusions ...*Client) {
	clients = Filter(clients, exclusions...)
	for _, client := range clients {
		go m.Unicast(client, packet)
	}
}

func (m *Manager) IsUsernameTaken(name string, excludeID snowflake.ID) bool {
	m.lock.Lock()
	defer m.lock.Unlock()
	for id, client := range m.connections {
		if id != excludeID && client.NameSet && client.Name == name {
			return true
		}
	}
	return false
}

func (m *Manager) FindClient(id any) (*Client, bool) {
	m.lock.Lock()
	defer m.lock.Unlock()

	// Try finding by snowflake ID first
	if sfID, ok := id.(snowflake.ID); ok {
		client, found := m.connections[sfID]
		return client, found
	}

	// Try finding by username
	idStr := fmt.Sprintf("%v", id) // Convert id to string for comparison
	for _, client := range m.connections {
		if client.NameSet && fmt.Sprintf("%v", client.Name) == idStr {
			return client, true
		}
	}

	// Try finding by UUID
	if uuidVal, err := uuid.Parse(idStr); err == nil {
		for _, client := range m.connections {
			if client.UUID == uuidVal {
				return client, true
			}
		}
	}

	return nil, false
}
