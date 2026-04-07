package server

import (
	"fmt"
	"log"
	"sync"

	"github.com/gofiber/contrib/websocket"
)

// Runner is the main loop of a ClientObject. It is responsible for reading and writing packets to and from the associated websocket connection.
// It also handles the exit channel, which is used to signal that the client should stop running.
// The function runs indefinitely until it receives a signal on the exit channel.
// During execution, it reads packets from the reader channel and writes packets to the writer channel.
// It also calls the Handler and Writer functions on separate goroutines to handle the packets.
// The function does not return until the client has stopped running.
func (c *Client) Runner() {
	go c.Reader()
	for {
		select {
		case <-c.exit:
			return
		case packet := <-c.reader:
			go c.Handler(packet)
		case packet := <-c.writer:
			go c.Writer(packet)
		}
	}
}

// Reader is a loop that reads packets from the associated websocket connection and writes them to the reader channel.
// If an error occurs while reading a packet, it logs the error and checks if the error is an unexpected close error.
// If it is, it signals that the client should stop running by closing the exit channel.
// If the packet is a text message, it attempts to parse the packet as JSON.
// If the parsing fails, it writes the packet as a string to the reader channel.
// If the parsing succeeds, it writes the parsed packet to the reader channel.
// The loop stops running if the exit channel is closed.
func (c *Client) Reader() {
reader:
	for {
		if msg_type, packet, err := c.conn.ReadMessage(); err != nil {
			log.Printf("%s %v", c.GiveName(), err)
			if websocket.IsCloseError(err) || websocket.IsUnexpectedCloseError(err) {
				c.exit <- true
				break reader
			}
		} else {
			switch msg_type {
			case websocket.TextMessage:

				// If this is the very first packet, try to detect the protocol
				if c.Protocol == nil {
					var ok bool
					if ok, c.Protocol = DetectAndReadProtocol(c, packet); !ok {
						c.writer <- "failed to detect protocol"
						c.exit <- true
						break reader
					}
				}

				// Otherwise, create a new reader instance and process it
				p := c.Protocol.New()
				if !p.Reader(packet) {
					c.writer <- "failed to parse packet"
					c.exit <- true
					break reader
				}
				c.reader <- p

			default:
				panic("unhandled message type")
			}
		}

		// Stop loop if c.exit is closed
		select {
		case <-c.exit:
			break reader
		default:
		}
	}
}

func DetectAndReadProtocol(c *Client, data []byte) (bool, Protocol) {
	var wg sync.WaitGroup
	resultCh := make(chan Protocol, 1) // Buffered channel of size 1

	detectors := []func(){
		func() {
			defer wg.Done()
			if p := NewCL3or4Packet(c); p.Reader(data) {
				log.Printf("%s 𝐢 CL3 or CL4 protocol detected", c.GiveName())
				resultCh <- p
			}
		},
		func() {
			defer wg.Done()
			if s := NewScratchPacket(c); s.Reader(data) {
				log.Printf("%s 𝐢 Scratch protocol detected", c.GiveName())
				resultCh <- s
			}
		},
		func() {
			defer wg.Done()
			if l := NewCL2Packet(c); l.Reader(data) {
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

func (c *Client) Writer(packet any) {
	c.tx.Lock()
	defer c.tx.Unlock()
	func() {
		switch transmit := packet.(type) {
		case Protocol:

			if c.manager.VeryVerbose {
				log.Printf("%s 🢀  %v", c.GiveName(), transmit.String())
			}

			if transmit.IsJSON() {
				c.conn.WriteMessage(websocket.TextMessage, transmit.Bytes())
			} else {
				c.conn.WriteMessage(websocket.TextMessage, []byte(transmit.String()))
			}

		case string:

			if c.manager.VeryVerbose {
				log.Printf("%s 🢀  %v", c.GiveName(), transmit)
			}

			c.conn.WriteMessage(websocket.TextMessage, []byte(transmit))
		case []byte:

			if c.manager.VeryVerbose {
				log.Printf("%s 🢀  %v", c.GiveName(), string(transmit))
			}

			c.conn.WriteMessage(websocket.TextMessage, transmit)
		default:
			panic("unhandled packet type")
		}
	}()
}

func (c *Client) Handler(packet any) {
	switch packet := packet.(type) {
	case Protocol:
		if c.manager.VeryVerbose {
			log.Printf("%s 🢂  %v", c.GiveName(), packet)
		}
		packet.Handler(c, c.manager)
	default:
		panic("unhandled protocol")
	}
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
