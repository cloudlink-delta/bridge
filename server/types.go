package server

import (
	"sync"

	"github.com/bwmarrin/snowflake"
	"github.com/cloudlink-delta/duplex"
	"github.com/gofiber/contrib/websocket"
	"github.com/google/uuid"
)

const (
	Protocol_Undefined = iota
	Protocol_Scratch
	Protocol_CL2
	Protocol_CL3or4
)

const (
	Dialect_Undefined = iota
	Dialect_CL2_Early
	Dialect_CL2_Late
	Dialect_CL3_0_1_5
	Dialect_CL3_0_1_7
	Dialect_CL4_0_1_8
	Dialect_CL4_0_1_9
	Dialect_CL4_0_2_0
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
	dialect   uint            `json:"-"`
	tx        *sync.Mutex     `json:"-"`
	manager   *Manager        `json:"-"`
	Protocol  `json:"-"`
}

type Room struct {
	Name       string
	GmsgState  any
	GvarStates map[any]any
	Clients    map[snowflake.ID]*Client
	manager    *Manager
	lock       *sync.Mutex
}

type UserObject struct {
	ID       snowflake.ID `json:"id,omitempty"`
	Username any          `json:"username,omitempty"`
	UUID     uuid.UUID    `json:"uuid,omitempty"`
}
