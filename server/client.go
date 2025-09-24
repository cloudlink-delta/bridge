package cloudlink

import (
	"github.com/bwmarrin/snowflake"
	"github.com/gofiber/contrib/websocket"
	"github.com/google/uuid"
)

// The client struct serves as a template for handling websocket sessions. It stores a client's UUID, Snowflake ID, manager and websocket connection pointer(s).
type Client struct {
	connection *websocket.Conn
	manager    *Manager
	id         snowflake.ID
	uuid       uuid.UUID
	username   any
	nameset    bool
	protocol   string
	dialect    int
	rooms      map[any]*Room
	handshake  bool
}

// Define protocols
const (
	Protocol_Detecting = ""
	Protocol_CL2       = "cl2"
	Protocol_CL4       = "cl4"
	Protocol_CloudVars = "cloudvar"
)

// Dialect constants for differentiating between CL protocol versions
const (
	Dialect_CL3_0_1_5 = iota // S2.2 compatible, no listeners/MOTD/statuscodes
	Dialect_CL3_0_1_7        // Supports early MOTD and statuscodes
	Dialect_CL4_0_1_8        // Supports listeners and rooms, but no handshake
	Dialect_CL4_0_1_9        // Implements the handshake command
	Dialect_CL4_0_2_0        // Implements native data types (autoConvert)
)

func (client *Client) SpoofServerVersion() string {
	switch client.dialect {
	case Dialect_CL3_0_1_5:
		return "0.1.5"
	case Dialect_CL3_0_1_7:
		return "0.1.7"
	case Dialect_CL4_0_1_8:
		return "0.1.8"
	case Dialect_CL4_0_1_9:
		return "0.1.9"
	case Dialect_CL4_0_2_0:
		return "0.2.0"
	default:
		return "0.1.5"
	}
}
