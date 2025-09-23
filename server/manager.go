package cloudlink

import (
	"log"
	"sync"

	"github.com/bwmarrin/snowflake"
	"github.com/gofiber/contrib/websocket"
	"github.com/google/uuid"
)

var ServerVersion string = "0.1.1-golang"

type Room struct {
	// Subscribed clients to the room
	clients      map[snowflake.ID]*Client
	clientsMutex sync.RWMutex

	// Global message (GMSG) state
	gmsgState      any
	gmsgStateMutex sync.RWMutex

	// Globar variables (GVAR) states
	gvarState      map[any]any
	gvarStateMutex sync.RWMutex

	// Friendly name for room
	name any

	// Locks states before subscribing/unsubscribing clients
	sync.RWMutex
}

type Manager struct {
	// Friendly name for manager
	name any

	// Registered client sessions
	clients      map[snowflake.ID]*Client
	clientsMutex sync.RWMutex

	// Rooms storage
	rooms      map[any]*Room
	roomsMutex sync.RWMutex

	// Configuration settings
	Config struct {
		RejectClients    bool
		CheckIPAddresses bool
		EnableMOTD       bool
		MOTDMessage      string
	}

	// Used for generating Snowflake IDs
	SnowflakeIDNode *snowflake.Node

	// Locks states before registering sessions
	sync.RWMutex
}

// NewClient assigns a UUID and Snowflake ID to a websocket client, and returns a initialized Client struct for use with a manager's AddClient.
func NewClient(conn *websocket.Conn, manager *Manager) *Client {
	// Request and create a lock before generating ID values
	manager.clientsMutex.Lock()

	// Generate client ID values
	client_id := manager.SnowflakeIDNode.Generate()
	client_uuid := uuid.New()

	// Release the lock
	manager.clientsMutex.Unlock()

	return &Client{
		connection: conn,
		manager:    manager,
		id:         client_id,
		uuid:       client_uuid,
		rooms:      make(map[any]*Room),
		handshake:  false,
	}
}

// Dummy Managers function identically to a normal manager. However, they are used for selecting specific clients to multicast to.
func DummyManager(name any) *Manager {
	return &Manager{
		clients: make(map[snowflake.ID]*Client),
		rooms:   make(map[any]*Room),
		name:    name,
	}
}

func New(name string) *Manager {
	node, err := snowflake.NewNode(1)
	if err != nil {
		log.Fatalln(err, 3)
	}

	manager := &Manager{
		name:            name,
		clients:         make(map[snowflake.ID]*Client),
		rooms:           make(map[any]*Room),
		SnowflakeIDNode: node,
	}

	return manager
}

func (manager *Manager) CreateRoom(name any) *Room {
	manager.roomsMutex.RLock()

	// Access rooms map
	_, exists := manager.rooms[name]

	manager.roomsMutex.RUnlock()

	if !exists {
		manager.roomsMutex.Lock()

		log.Printf("[%s] Creating room %s", manager.name, name)

		// Create and prepare the room state
		manager.rooms[name] = &Room{
			name:      name,
			clients:   make(map[snowflake.ID]*Client, 1),
			gmsgState: "",
			gvarState: make(map[any]any),
		}

		manager.roomsMutex.Unlock()
	}

	// Return the room even if it already exists
	return manager.rooms[name]
}

func (room *Room) SubscribeClient(client *Client) {
	room.clientsMutex.Lock()

	// Add client
	room.clients[client.id] = client

	room.clientsMutex.Unlock()
	client.Lock()

	// Add pointer to subscribed room in client's state
	client.rooms[room.name] = room

	client.Unlock()

	// Handle CL room states
	client.RLock()
	protocol := client.protocol
	usernameset := (client.username != nil)
	client.RUnlock()
	if protocol == Protocol_CL4 && usernameset {
		room.BroadcastUserlistEvent("add", client, false)
	}
}

func (room *Room) UnsubscribeClient(client *Client) {
	room.clientsMutex.Lock()

	// Remove client
	delete(room.clients, client.id)

	room.clientsMutex.Unlock()
	client.Lock()

	// Remove pointer to subscribed room from client's state
	delete(client.rooms, room.name)

	client.Unlock()

	// Handle CL room states
	client.RLock()
	protocol := client.protocol
	usernameset := (client.username != nil)
	client.RUnlock()
	if protocol == Protocol_CL4 && usernameset {
		room.BroadcastUserlistEvent("remove", client, true)
	}
}

func (manager *Manager) DeleteRoom(name any) {
	manager.roomsMutex.Lock()

	log.Printf("[%s] Destroying room %s", manager.name, name)

	// Delete room
	delete(manager.rooms, name)

	manager.roomsMutex.Unlock()
}

func (manager *Manager) AddClient(client *Client) {
	manager.clientsMutex.Lock()

	// Add client
	manager.clients[client.id] = client

	manager.clientsMutex.Unlock()
}

func (manager *Manager) RemoveClient(client *Client) {
	manager.clientsMutex.Lock()

	// Remove client from manager's clients map
	delete(manager.clients, client.id)

	// Unsubscribe from all rooms and free memory by clearing out empty rooms
	for _, room := range TempCopyRooms(client.rooms) {
		room.UnsubscribeClient(client)

		// Destroy room if empty, but don't destroy default room
		if len(room.clients) == 0 && (room.name != "default") {
			manager.DeleteRoom(room.name)
		}
	}
	manager.clientsMutex.Unlock()
}

// findClientByUsername iterates through all clients in the manager to find one by its username.
func findClientByUsername(manager *Manager, username any) (*Client, bool) {
	manager.clientsMutex.RLock()
	defer manager.clientsMutex.RUnlock()

	for _, client := range manager.clients {
		client.RLock()
		// Check if the username is set and matches
		if client.username != nil && client.username == username {
			client.RUnlock()
			return client, true
		}
		client.RUnlock()
	}
	return nil, false
}

// getAllUsernamesInRoom collects the usernames of all clients subscribed to a specific room.
func getAllUsernamesInRoom(room *Room) []string {
	room.clientsMutex.RLock()
	defer room.clientsMutex.RUnlock()

	usernames := make([]string, 0, len(room.clients))
	for _, client := range room.clients {
		client.RLock()
		if client.username != nil {
			if name, ok := client.username.(string); ok {
				usernames = append(usernames, name)
			}
		}
		client.RUnlock()
	}
	return usernames
}

// getClientGroupsByHandshake splits a room's clients into two groups based on their handshake status.
func getClientGroupsByHandshake(room *Room) (specialClients, standardClients map[snowflake.ID]*Client) {
	specialClients = make(map[snowflake.ID]*Client)
	standardClients = make(map[snowflake.ID]*Client)

	room.clientsMutex.RLock()
	defer room.clientsMutex.RUnlock()

	for id, client := range room.clients {
		client.RLock()
		if client.handshake {
			specialClients[id] = client
		} else {
			standardClients[id] = client
		}
		client.RUnlock()
	}
	return specialClients, standardClients
}
