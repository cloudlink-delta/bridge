package cloudlink

import (
	"log"
	"strings"
	"sync"

	"github.com/bwmarrin/snowflake"
	"github.com/goccy/go-json"
)

func (c *Client) AllRooms() []*Room {
	c.roomlock.Lock()
	defer c.roomlock.Unlock()
	var room_map []*Room
	for _, room := range c.Rooms {
		room_map = append(room_map, room)
	}
	return room_map
}

func (m *Manager) AllRooms() []*Room {
	m.lock.Lock()
	defer m.lock.Unlock()
	var room_map []*Room
	for _, room := range m.rooms {
		room_map = append(room_map, room)
	}
	return room_map
}

func (m *Manager) CreateRoom(name string) *Room {
	if res := m.GetRoom(name); res != nil {
		return res
	}

	m.lock.Lock()
	defer m.lock.Unlock()
	room := &Room{
		Name:       name,
		manager:    m,
		Clients:    make(map[snowflake.ID]*Client),
		GmsgState:  "",
		GvarStates: make(map[any]any),
		lock:       &sync.Mutex{},
	}

	m.rooms[name] = room

	if m.VeryVerbose {
		log.Println("Created room", room.Name)
	}

	return room
}

func (m *Manager) DestroyRoom(room *Room) {
	m.lock.Lock()
	defer m.lock.Unlock()
	delete(m.rooms, room.Name)
}

func (m *Manager) GetRoom(name string) *Room {
	m.lock.Lock()
	defer m.lock.Unlock()
	room, ok := m.rooms[name]
	if ok {
		return room
	}
	return nil
}

func (c *Client) JoinRoom(r *Room) {
	r.lock.Lock()
	r.Clients[c.ID] = c
	r.lock.Unlock()

	c.roomlock.Lock()
	c.Rooms[r.Name] = r
	c.roomlock.Unlock()

	log.Printf("%s joined room %s", c.GiveName(), r.Name)
}

func (c *Client) LeaveRoom(r *Room) {
	m := c.manager

	r.lock.Lock()
	delete(r.Clients, c.ID)
	r.lock.Unlock()

	c.roomlock.Lock()
	delete(c.Rooms, r.Name)
	c.roomlock.Unlock()

	log.Printf("%s left room %s", c.GiveName(), r.Name)

	// Destroy room if empty but never the default one
	if r != m.DefaultRoom && len(r.Clients) == 0 {
		log.Println("Destroying room", r.Name, "because it has been deserted")
		m.DestroyRoom(r)
	}
}

func (r *Room) ClientsAsSlice() []*Client {
	r.lock.Lock()
	defer r.lock.Unlock()
	var clients []*Client
	for _, client := range r.Clients {
		clients = append(clients, client)
	}
	return clients
}

func (r *Room) SetGvar(name any, value any) {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.GvarStates[name] = value
}

func (r *Room) GetGvar(name any) any {
	r.lock.Lock()
	defer r.lock.Unlock()
	return r.GvarStates[name]
}

func (r *Room) DeleteGvar(name any) {
	r.lock.Lock()
	defer r.lock.Unlock()
	delete(r.GvarStates, name)
}

func (r *Room) GetAllGvars() map[any]any {
	r.lock.Lock()
	defer r.lock.Unlock()
	return r.GvarStates
}

func (r *Room) GenerateUserlistString() string {
	r.lock.Lock()
	defer r.lock.Unlock()
	var sb strings.Builder
	for _, s := range r.Clients {
		e, err := json.Marshal(s.Name)
		if err != nil {
			log.Println(err)
			continue
		}
		sb.WriteString(string(e)[1 : len(e)-1])
		sb.WriteRune(';')
	}
	return sb.String()
}

func (r *Room) GenerateUserlistSlice() any {
	r.lock.Lock()
	defer r.lock.Unlock()
	var usernames []string
	for _, client := range r.Clients {
		if !client.NameSet {
			continue
		}
		e, err := json.Marshal(client.Name)
		if err != nil {
			log.Println(err)
			continue
		}
		usernames = append(usernames, string(e))
	}
	if len(usernames) == 0 {
		return "[]"
	}
	return usernames
}

func (r *Room) GenerateUserObjectList() any {
	r.lock.Lock()
	defer r.lock.Unlock()
	var users []*UserObject
	for _, client := range r.Clients {
		if !client.NameSet {
			continue
		}
		users = append(users, client.GetUserObject())
	}
	if len(users) == 0 {
		return "[]"
	}
	return users
}

func (r *Room) GetHandshakedClients() []*Client {
	r.lock.Lock()
	defer r.lock.Unlock()
	var clients []*Client
	for _, client := range r.Clients {
		if !client.Handshake {
			continue
		}
		clients = append(clients, client)
	}
	return clients
}

func (r *Room) FindClientByUsername(username string) (target *Client, found bool) {
	r.lock.Lock()
	defer r.lock.Unlock()
	for _, client := range r.Clients {
		if client.Name == username {
			return client, true
		}
	}
	return nil, false
}
