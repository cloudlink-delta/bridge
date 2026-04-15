package server

import (
	"sync"
	"time"

	"github.com/bwmarrin/snowflake"
	"github.com/cloudlink-delta/duplex"
	"github.com/goccy/go-json"
	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/kaptinlin/jsonschema"
)

const DEFAULT_ROOM RoomKey = "default"

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

type SocketCodes struct {
	Code    uint
	Message string
}

var (
	Generic_Error              = SocketCodes{4000, "Generic Error"}
	Username_Error             = SocketCodes{4002, "Username Error"}
	Overloaded_Status          = SocketCodes{4003, "Overloaded"}
	Unavailable_Status         = SocketCodes{4004, "Project Unavailable"}
	Security_Error             = SocketCodes{4005, "Closed to protect your security"}
	Identity_Error             = SocketCodes{4006, "Identify yourself"}
	Protocol_Detection_Failure = SocketCodes{4007, "Protocol detection failed"}
	Protocol_Handler_Failure   = SocketCodes{4008, "Protocol handler failed"}
	Ratelimit_Exceeded         = SocketCodes{4009, "Packet ratelimit has been exceeded"}
)

type Room struct {
	Clients    Targets
	GlobalVars sync.Map // Protocol-agnostic global variable storage
}

type Targets map[*BridgeClient]bool
type BridgeClients []*BridgeClient

type Protocol interface {

	// Helper that runs automatically to clean up any remaining states for a detected protocol once a client disconnects
	On_Disconnect(*BridgeClient, RoomKeys)

	// Downgrades or transforms a packet's structure to match the target client's dialect.
	Apply_Quirks(*BridgeClient, any) any

	// Lead-in function that detects a client's protocol and dialect, and processes the packet if a protocol match was made.
	Reader(*BridgeClient, []byte) bool
}

type Config struct {

	// Enables the Message of the Day. Will be shared with compatible clients.
	Enable_MOTD bool

	// If enabled, this value will be used as the MOTD.
	MOTD_Message string

	// If enabled, compatible clients will be read back their IP address.
	Serve_IP_Addresses bool

	// The maximum number of concurrently opened rooms. Cannot be less than or equal to zero.
	Maximum_Rooms uint

	// The maximum number of concurrently connected clients. Cannot be less than or equal to zero.
	Maximum_Clients uint

	// If enabled, the server will use a patch that forces the "set" method for ulist events.
	// It is more network heavy to use instead of incremental updates (the default behavior),
	// But it addresses unfixed bugs with older CL clients.
	Force_Set bool

	// Defines the listening address of the WebSocket bridge.
	Address string

	// Rate limiting: Maximum number of messages per interval.
	Rate_Limit_Burst int

	// Rate limiting: The interval at which the message count resets.
	Rate_Limit_Interval time.Duration
}

type Server struct {
	Self               string
	Close              chan bool
	Done               chan bool
	Config             *Config
	instance           *duplex.Instance
	deltaclientsmu     sync.RWMutex
	ClassicClients     Targets
	classicclientsmu   sync.RWMutex
	RoomsMap           map[RoomKey]*Room // Replaces clients map
	roomsMu            sync.RWMutex      // Replaces clientsMu
	snowflakeGen       *snowflake.Node
	App                *fiber.App
	Address            string
	DeltaResolverCache map[*duplex.Peer]HelloArgs
}

type CLDelta struct {
	*Server
}

// CL4_or_CL3 implements the Protocol interface
type CL4_or_CL3 struct {
	Schema *jsonschema.Schema
	*Server
}

type CL4_UserObject struct {
	ID       string `json:"id"`
	UUID     string `json:"uuid"`
	Username any    `json:"username,omitempty"`
}

// Common_Packet is the internal packet format that the bridge's WebSocket gateway natively understands.
type Common_Packet struct {
	Command   string `json:"cmd" jsonschema:"required"`
	Name      any    `json:"name,omitempty"`
	Data      any    `json:"data,omitempty"` // For CL3 0.1.5 dialect
	Value     any    `json:"val,omitempty"`
	ID        any    `json:"id,omitempty"`    // Recipient(s) for pmsg/pvar from client
	Rooms     any    `json:"rooms,omitempty"` // Target room(s) for gmsg/gvar from client, or context for server->client
	Listener  any    `json:"listener,omitempty"`
	Code      string `json:"code,omitempty"`
	CodeID    int    `json:"code_id,omitempty"`
	Mode      string `json:"mode,omitempty"`   // For ulist updates
	Origin    any    `json:"origin,omitempty"` // For server->client responses, contains UserObject
	Details   any    `json:"details,omitempty"`
	Recipient any    `json:"recipient,omitempty"` // Legacy/alternative for ID
}

func (p *Common_Packet) String() string {
	b, err := json.Marshal(p)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// ScratchPacket represents a Cloud Variable protocol command.
type ScratchPacket struct {
	Method    string `json:"method" jsonschema:"required"`
	ProjectID string `json:"project_id,omitempty"`
	User      string `json:"user,omitempty"`
	Name      any    `json:"name,omitempty"`
	NewName   any    `json:"new_name,omitempty"`
	Value     any    `json:"value,omitempty"`
}

func (p *ScratchPacket) String() string {
	b, err := json.Marshal(p)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// CL2Packet represents a parsed incoming CL2 command.
type CL2Packet struct {
	Command   string `json:"cmd,omitempty"`
	Mode      string `json:"mode,omitempty"`
	Sender    string `json:"sender,omitempty"` // Parsed from packet
	Recipient string `json:"recipient,omitempty"`
	Var       any    `json:"var,omitempty"`  // For variable commands
	Type      string `json:"type,omitempty"` // For JSON response building
	Data      any    `json:"data,omitempty"` // Parsed data or data for JSON response
	ID        string `json:"id,omitempty"`   // For private JSON responses
}

// CL2Packet_TxReply represents the outer structure for outgoing JSON responses.
type CL2Packet_TxReply struct {
	Type string `json:"type"`         // e.g., "gs", "ps", "sf", "ul"
	Data any    `json:"data"`         // Can be string or nested CL2Packet_TxData
	ID   string `json:"id,omitempty"` // Recipient ID for "ps" type
}

// CL2Packet_TxData represents the nested 'data' object for special feature responses.
type CL2Packet_TxData struct {
	Type string `json:"type,omitempty"` // e.g., "gs", "ps", "vm", "vers"
	Mode string `json:"mode,omitempty"` // "g" or "p" for "vm" type
	Var  any    `json:"var,omitempty"`  // Variable name for "vm" type
	Data any    `json:"data"`           // Actual payload
}

func (p *CL2Packet) String() string {
	b, err := json.Marshal(p)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// QueryAck is a clone of the one present in the Discovery service. It is a response template to the "QUERY" opcode.
type QueryAck struct {
	Online        bool   `json:"online"`
	Username      string `json:"username,omitempty"`
	Designation   string `json:"designation,omitempty"`
	InstanceID    string `json:"instance_id,omitempty"`
	IsLobbyMember bool   `json:"is_lobby_member,omitempty"`
	IsLobbyHost   bool   `json:"is_lobby_host,omitempty"`
	IsInLobby     bool   `json:"is_in_lobby,omitempty"`
	LobbyID       string `json:"lobby_id,omitempty"`
	RTT           int64  `json:"rtt,omitempty"`
	IsLegacy      bool   `json:"is_legacy,omitempty"`
	IsRelayed     bool   `json:"is_relayed,omitempty"`
	RelayPeer     string `json:"relay_peer,omitempty"`
	IsBridge      bool   `json:"is_bridge,omitempty"`
	IsDiscovery   bool   `json:"is_discovery,omitempty"`
}

type HelloArgs struct {
	Name        string `json:"name"`
	Designation string `json:"designation"`
}

type RoomKey string
type RoomKeys []RoomKey

type BridgeClient struct {
	Conn     *websocket.Conn
	ID       string
	Peer     *duplex.Peer
	UUID     string
	Username any
	writer   chan []byte
	exit     chan bool
	Rooms    RoomKeys
	room_mux sync.RWMutex
	dialect  uint
	Protocol Protocol
	Server   *Server

	// Rate limiting
	last_msg_time time.Time
	msg_count     int
}
