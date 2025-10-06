package cloudlink

import (
	"log"
	"sync"
)

const (
	Protocol_Undefined = iota
	Protocol_Scratch
	Protocol_CL2
	Protocol_CL3or4
)

const (
	Dialect_Undefined = iota
	Dialect_CL3_0_1_5
	Dialect_CL3_0_1_7
	Dialect_CL4_0_1_8
	Dialect_CL4_0_1_9
	Dialect_CL4_0_2_0
)

// NewProtocol takes a protocol number and returns a pointer to a Protocol object
// representing the given protocol. If the protocol is unknown, it returns nil.
// The returned Protocol object is a pointer to a struct that implements the
// Protocol interface, which defines methods for parsing, interpreting, and
// handling packets.
// Supported protocols are:
//
//	Protocol_Scratch: Scratch protocol
//	Protocol_CL2: CL2 protocol
//	Protocol_CL3or4: CL3 or CL4 protocol
func NewProtocol(protocol uint) Protocol {
	switch protocol {
	case Protocol_Scratch:
		return &ScratchPacket{}
	case Protocol_CL2:
		return &CL2Packet{}
	case Protocol_CL3or4:
		return &CL3or4Packet{}
	default:
		return nil
	}
}

// Protocol is a generic interface that defines the methods required
// for parsing, interpreting, and handling packets.
type Protocol interface {

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
}

// This interface defines common methods that can be translated between
// various versions of the CL protocol.
type CloudLink interface {

	// MulticastGmsg sends a message to all connected clients in a room.
	MulticastGmsg(string)

	// MulticastGvar sends a variable to all connected clients in a room.
	MulticastGvar(string, any)

	// UnicastGmsg sends a message to a specific client.
	UnicastGmsg(*Client, string)

	// UnicastGvar sends a variable to a specific client.
	UnicastGvar(*Client, string, any)

	// SendHandshake sends a handshake reply to the client.
	SendHandshake(*Client)
}

// DetectAndReadProtocol takes a raw byte array and returns a tuple of a boolean indicating whether a valid protocol was detected, and a pointer to a Protocol object representing the detected protocol.
// The function uses goroutines to concurrently detect CL2, CL3or4, and Scratch packets, and returns the first valid protocol detected.
// It checks for protocols in a specific order: CL3/4, then Scratch, then CL2.
// If no valid protocol is detected, it returns false and a nil Protocol pointer.
func DetectAndReadProtocol(data []byte, c *Client) (bool, Protocol) {
	var wg sync.WaitGroup
	resultCh := make(chan Protocol, 1) // Buffered channel of size 1

	detectors := []func(){
		func() {
			defer wg.Done()
			if p := new(CL3or4Packet); p.Reader(data) {
				log.Printf("%s 𝐢 CL3 or CL4 protocol detected", c.GiveName())
				resultCh <- p
			}
		},
		func() {
			defer wg.Done()
			if s := new(ScratchPacket); s.Reader(data) {
				log.Printf("%s 𝐢 Scratch protocol detected", c.GiveName())
				resultCh <- s
			}
		},
		func() {
			defer wg.Done()
			if l := new(CL2Packet); l.Reader(data) {
				log.Printf("%s 𝐢 CL2 protocol detected", c.GiveName())
				resultCh <- l
			}
		},
	}

	wg.Add(len(detectors))
	for _, detector := range detectors {
		go detector()
	}

	// Goroutine to wait for all detectors and close the channel
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	if p, ok := <-resultCh; ok {
		return true, p
	}

	// No valid protocol detected
	log.Println("No valid protocol detected")
	return false, nil
}

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
