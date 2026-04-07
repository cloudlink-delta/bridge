package server

type Protocol interface {
	// Creates a blank instance of a protocol for reading.
	New() Protocol

	// Sends a message to all specified clients.
	BroadcastMsg([]*Client, any)

	// Sends a variable to all specified clients.
	BroadcastVar([]*Client, any, any)

	// Sends a message to a specific client.
	UnicastMsg(*Client, any)

	// Sends a variable to a specific client.
	UnicastVar(*Client, any, any)

	// Defines a binder for translation.
	BroadcastBinder() chan any
	UnicastBinder() chan any

	// Reader takes a raw byte array and returns a boolean indicating whether the packet is valid.
	// It also pre-formats the packet into a structured form based on the protocol type.
	Reader(data []byte) bool

	// Bytes returns a byte array representation of the packet.
	Bytes() []byte

	// String returns a string representation of the packet.
	String() string

	// DeriveProtocol returns the protocol number for the packet.
	DeriveProtocol() uint

	// DeriveDialect returns the dialect number for the packet.
	DeriveDialect(*Client) uint

	// Handler processes the packet and performs any necessary actions.
	Handler(*Client, *Manager)

	// IsJSON returns a boolean indicating whether the protocol uses JSON.
	IsJSON() bool

	// Returns a spoofed server string to maintain compatibility based on the detected protocol and implementation.
	SpoofServerVersion() string

	// ToGeneric converts a protocol-specific format into a commmon format that can be translated between other protocols.
	ToGeneric() *Generic

	// FromGeneric translates common formats into a protocol-specific format.
	FromGeneric(*Generic)
}
