package cloudlink

import (
	"fmt"
	"log"

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
	Protocol_CL3or4    = "cl4"
	Protocol_CloudVars = "cloudvar"
)

// Dialect constants for differentiating between CL protocol versions
const (
	Dialect_Determining = iota
	Dialect_CL3_0_1_5   // S2.2 compatible, no listeners/MOTD/statuscodes
	Dialect_CL3_0_1_7   // Supports early MOTD and statuscodes
	Dialect_CL4_0_1_8   // Supports listeners and rooms, but no handshake
	Dialect_CL4_0_1_9   // Implements the handshake command
	Dialect_CL4_0_2_0   // Implements native data types (autoConvert)
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

// Generates a value for client identification.
func (client *Client) GenerateUserObject() *UserObject {
	if client.username != nil {
		return &UserObject{
			Id:       fmt.Sprint(client.id),
			Username: client.username,
			Uuid:     fmt.Sprint(client.uuid),
		}
	} else {
		return &UserObject{
			Id:   fmt.Sprint(client.id),
			Uuid: fmt.Sprint(client.uuid),
		}
	}
}

func (client *Client) UpgradeDialect(newdialect int) {
	if newdialect > client.dialect {

		var basestring string
		if client.dialect == Dialect_Determining {
			basestring = fmt.Sprintf("Client %s (%s) detected ", client.id, client.uuid)
		} else {
			basestring = fmt.Sprintf("Client %s (%s) upgraded to ", client.id, client.uuid)
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

func (client *Client) DetectDialect(cl4packet *Packet_UPL) {

	// Detect dialect
	if cl4packet.Cmd == "handshake" {

		// Check for the new v0.2.0 handshake format
		if valMap, ok := cl4packet.Val.(map[string]any); ok {
			_, langExists := valMap["language"]
			_, versExists := valMap["version"]
			if langExists && versExists {
				client.UpgradeDialect(Dialect_CL4_0_2_0)
			} else {
				client.UpgradeDialect(Dialect_CL4_0_1_9)
			}
		} else {
			client.UpgradeDialect(Dialect_CL4_0_1_9)
		}

	} else if cl4packet.Cmd == "link" || (cl4packet.Listener != nil && cl4packet.Listener != "") {
		client.UpgradeDialect(Dialect_CL4_0_1_8)

	} else if cl4packet.Cmd == "direct" && isTypeDeclaration(cl4packet.Val) {
		client.UpgradeDialect(Dialect_CL3_0_1_7)

	} else {
		client.UpgradeDialect(Dialect_CL3_0_1_5)
	}
}
