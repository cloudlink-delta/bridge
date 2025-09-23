package cloudlink

import (
	"sync"

	"github.com/bwmarrin/snowflake"
	"github.com/gofiber/contrib/websocket"
	"github.com/google/uuid"
)

// The client struct serves as a template for handling websocket sessions. It stores a client's UUID, Snowflake ID, manager and websocket connection pointer(s).
type Client struct {
	connection      *websocket.Conn
	connectionMutex sync.RWMutex
	manager         *Manager
	id              snowflake.ID
	uuid            uuid.UUID
	username        any
	protocol        string
	rooms           map[any]*Room
	handshake       bool

	// Lock state for rooms
	sync.RWMutex
}

// Define protocols
const (
	Protocol_Detecting = ""
	Protocol_CL2       = "cl2"
	Protocol_CL3       = "cl3"
	Protocol_CL4       = "cl4"
	Protocol_CloudVars = "cloudvar"
)
