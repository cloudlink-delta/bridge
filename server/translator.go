package server

const (
	Generic_NOOP = iota
	Generic_Broadcast
	Generic_Unicast
	Generic_Multicast
	Generic_Userlist_Event
	Generic_Direct
)

type Generic struct {
	Opcode  uint
	Payload any
	Target  *Client
	Origin  *Client
}
