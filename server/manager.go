package cloudlink

import (
	"log"
	"maps"

	"github.com/bwmarrin/snowflake"
	"github.com/gofiber/contrib/websocket"
	"github.com/google/uuid"
)

var ServerVersion string = "0.1.1-golang"

type Room struct {
	// Subscribed clients to the room
	clients map[snowflake.ID]*Client

	// Global message (GMSG) state
	gmsgState any

	// Globar variables (GVAR) states
	gvarState map[any]any

	// Friendly name for room
	name any
}

type Manager struct {
	// Friendly name for manager
	name any

	// Registered client sessions
	clients map[snowflake.ID]*Client

	// Rooms storage
	rooms map[any]*Room

	// Configuration settings
	Config struct {
		RejectClients    bool
		CheckIPAddresses bool
		EnableMOTD       bool
		MOTDMessage      string
	}

	// Used for generating Snowflake IDs
	SnowflakeIDNode *snowflake.Node
}

// NewClient assigns a UUID and Snowflake ID to a websocket client, and returns a initialized Client struct for use with a manager's AddClient.
func NewClient(conn *websocket.Conn, manager *Manager) *Client {
	// Generate client ID values
	client_id := manager.SnowflakeIDNode.Generate()
	client_uuid := uuid.New()

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
		log.Fatal(err)
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
	// Access rooms map
	_, exists := manager.rooms[name]

	if !exists {

		log.Printf("[%s] Creating room %s", manager.name, name)

		// Create and prepare the room state
		manager.rooms[name] = &Room{
			name:      name,
			clients:   make(map[snowflake.ID]*Client, 1),
			gmsgState: "",
			gvarState: make(map[any]any),
		}

	} else {
		log.Printf("[%s] Room %s already exists", manager.name, name)
	}

	// Return the room even if it already exists
	return manager.rooms[name]
}

func (room *Room) SubscribeClient(client *Client) {

	// Add client
	room.clients[client.id] = client

	// Add pointer to subscribed room in client's state
	client.rooms[room.name] = room

	// Handle CL room states
	protocol := client.protocol
	usernameset := (client.username != nil)
	if protocol == Protocol_CL4 && usernameset {
		room.BroadcastUserlistEvent("add", client, false)
	}
}

func (room *Room) UnsubscribeClient(client *Client) {

	// Remove client
	delete(room.clients, client.id)

	// Remove pointer to subscribed room from client's state
	delete(client.rooms, room.name)

	// Handle CL room states
	protocol := client.protocol
	usernameset := (client.username != nil)
	if protocol == Protocol_CL4 && usernameset {
		room.BroadcastUserlistEvent("remove", client, true)
	}
}

func (manager *Manager) DeleteRoom(name any) {
	log.Printf("[%s] Destroying room %s", manager.name, name)

	// Delete room
	delete(manager.rooms, name)
}

func (manager *Manager) AddClient(client *Client) {

	// Add client
	manager.clients[client.id] = client
}

func (manager *Manager) RemoveClient(client *Client) {

	// Remove client from manager's clients map
	delete(manager.clients, client.id)

	// Unsubscribe from all rooms and free memory by clearing out empty rooms
	temp := map[any]*Room{}
	maps.Copy(temp, client.rooms)
	for _, room := range temp {
		room.UnsubscribeClient(client)

		// Destroy room if empty, but don't destroy default room
		if len(room.clients) == 0 && (room.name != "default") {
			manager.DeleteRoom(room.name)
		}
	}
}

// findClientByUsername iterates through all clients in the manager to find one by its username.
func findClientByUsername(manager *Manager, username any) (*Client, bool) {
	for _, client := range manager.clients {
		// Check if the username is set and matches
		if client.username != nil && client.username == username {
			return client, true
		}
	}
	return nil, false
}

// getAllUsernamesInRoom collects the usernames of all clients subscribed to a specific room.
func getAllUsernamesInRoom(room *Room) []string {
	usernames := make([]string, 0, len(room.clients))
	for _, client := range room.clients {
		if client.username != nil {
			if name, ok := client.username.(string); ok {
				usernames = append(usernames, name)
			}
		}
	}
	return usernames
}

// getClientGroupsByHandshake splits a room's clients into two groups based on their handshake status.
func getClientGroupsByHandshake(room *Room) (specialClients, standardClients map[snowflake.ID]*Client) {
	specialClients = make(map[snowflake.ID]*Client)
	standardClients = make(map[snowflake.ID]*Client)

	for id, client := range room.clients {
		if client.handshake {
			specialClients[id] = client
		} else {
			standardClients[id] = client
		}
	}
	return specialClients, standardClients
}
