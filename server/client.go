package cloudlink

import (
	"fmt"
	"log"
	"sync"

	"github.com/bwmarrin/snowflake"
	"github.com/gofiber/contrib/websocket"
	"github.com/google/uuid"
)

type Client struct {
	ID        snowflake.ID    `json:"id,omitempty"`
	UUID      uuid.UUID       `json:"uuid,omitempty"`
	Name      any             `json:"username,omitempty"`
	NameSet   bool            `json:"nameset"`
	Handshake bool            `json:"handshake"`
	Rooms     map[any]*Room   `json:"rooms,omitempty"`
	roomlock  *sync.Mutex     `json:"-"`
	conn      *websocket.Conn `json:"-"`
	exit      chan bool       `json:"-"`
	reader    chan any        `json:"-"`
	writer    chan any        `json:"-"`
	protocol  uint            `json:"-"`
	dialect   uint            `json:"-"`
	tx        *sync.Mutex     `json:"-"`
	manager   *Manager        `json:"-"`
}

func (c *Client) GetUserObject() *UserObject {
	if c.NameSet {
		return &UserObject{
			ID:       c.ID,
			Username: c.Name,
			UUID:     c.UUID,
		}
	} else {
		return &UserObject{
			ID:   c.ID,
			UUID: c.UUID,
		}
	}
}

func (client *Client) UpgradeDialect(newdialect uint) {
	if newdialect > client.dialect {

		var basestring string
		if client.dialect == Dialect_Undefined {
			basestring = fmt.Sprintf("%s 𝐢 Detected ", client.GiveName())
		} else {
			basestring = fmt.Sprintf("%s 𝐢 Upgraded to ", client.GiveName())
		}

		client.dialect = newdialect
		switch client.dialect {
		case Dialect_CL3_0_1_5:
			log.Println(basestring + "CL3 dialect v0.1.5")
		case Dialect_CL3_0_1_7:
			log.Println(basestring + "CL3 dialect v0.1.7")
		case Dialect_CL4_0_1_8:
			log.Println(basestring + "CL4 dialect v0.1.8")
		case Dialect_CL4_0_1_9:
			log.Println(basestring + "CL4 dialect v0.1.9")
		case Dialect_CL4_0_2_0:
			log.Println(basestring + "CL4 dialect v0.2.0")
		}
	}
}

func (c *Client) GiveName() string {
	if !c.NameSet {
		return fmt.Sprintf("[%s]", c.ID)
	} else {
		return fmt.Sprintf("[%v (%s)]", c.Name, c.ID)
	}
}

func (c *Client) SetName(name any) {
	c.Name = name
	c.NameSet = true
	log.Printf("[%s] ▶ [%v (%s)]", c.ID, name, c.ID)
}

func (c *Client) UpdateHandshake(handshake bool) {
	c.Handshake = handshake
}

func (c *Client) AllRooms() []*Room {
	c.roomlock.Lock()
	defer c.roomlock.Unlock()
	var room_map []*Room
	for _, room := range c.Rooms {
		room_map = append(room_map, room)
	}
	return room_map
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
