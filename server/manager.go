package cloudlink

import (
	"log"
	"sync"

	"github.com/bwmarrin/snowflake"
	"github.com/gofiber/contrib/websocket"
	"github.com/google/uuid"
)

type Manager struct {
	node          *snowflake.Node
	connections   map[snowflake.ID]*Client
	rooms         map[string]*Room
	lock          *sync.Mutex
	DefaultRoom   *Room
	ServerVersion string
	VeryVerbose   bool
}

func NewManager() *Manager {

	// Initialize snowflake node
	node, err := snowflake.NewNode(1)
	if err != nil {
		panic(err)
	}

	// Initialize manager
	manager := &Manager{
		node:          node,
		connections:   make(map[snowflake.ID]*Client),
		rooms:         make(map[string]*Room),
		lock:          &sync.Mutex{},
		ServerVersion: "1.2.0",
		VeryVerbose:   true,
	}

	// Create default room
	manager.DefaultRoom = manager.CreateRoom("default")

	return manager
}

func (m *Manager) Create(conn *websocket.Conn) *Client {
	c := &Client{
		ID:       m.node.Generate(),
		UUID:     uuid.New(),
		Rooms:    make(map[string]*Room),
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
