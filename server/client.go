package server

import (
	"fmt"
	"log"
	"sync"

	"github.com/gofiber/contrib/websocket"
)

// Writer listens to the Sender channel and writes messages to the WebSocket.
// It ensures only one goroutine ever writes to the connection at a time.
func (c *ClassicClient) Writer() {
	defer c.Conn.Close()
	for p := range c.writer {
		log.Printf("%s 🢀  %v", c.GiveName(), string(p))
		if err := c.Conn.WriteMessage(websocket.TextMessage, p); err != nil {
			log.Printf("Error writing to client %s: %v", c.ID, err)
			break
		}
	}
}

func (c *ClassicClient) Reader() {
reader:
	for {
		if msg_type, packet, err := c.Conn.ReadMessage(); err != nil {
			log.Printf("%s %v", c.GiveName(), err)
			if websocket.IsCloseError(err) || websocket.IsUnexpectedCloseError(err) {
				c.exit <- true
				break reader
			}
		} else {
			switch msg_type {
			case websocket.TextMessage:
				switch p := c.Protocol.(type) {
				case nil:
					if p, ok := c.DetectAndReadProtocol(packet); !ok {
						c.writer <- []byte("failed to detect protocol")
						c.exit <- true
						break reader
					} else {
						c.Protocol = p
					}
				case *CL4_or_CL3:
					p.Reader(c, packet)
				case *Scratch_Handler:
					p.Reader(c, packet)
				case *CL2:
					p.Reader(c, packet)
				default:
					panic("unhandled protocol")
				}

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

func (c *ClassicClient) GiveName() string {
	// If the username is nil or empty, return just the Snowflake ID
	if c.Username == nil || c.Username == "" {
		return fmt.Sprintf("[%s]", c.ID)
	}
	// If they have a username, include it with the Snowflake ID
	return fmt.Sprintf("[%v (%s)]", c.Username, c.ID)
}

func (c *ClassicClient) DetectAndReadProtocol(data []byte) (Protocol, bool) {
	var wg sync.WaitGroup
	resultCh := make(chan Protocol, 1) // Buffered channel of size 1

	detectors := []func(){
		func() {
			defer wg.Done()
			p := New_CL4_or_CL3(c.Server)
			if p.Reader(c, data) {
				log.Printf("%s 𝐢 CL3 or CL4 protocol detected", c.GiveName())
				c.Protocol = p
				resultCh <- p
			}
		},
		func() {
			defer wg.Done()
			p := New_Scratch(c.Server)
			if p.Reader(c, data) {
				log.Printf("%s 𝐢 Scratch protocol detected", c.GiveName())
				c.Protocol = p
				resultCh <- p
			}
		},
		func() {
			defer wg.Done()
			p := New_CL2(c.Server)
			if p.Reader(c, data) {
				log.Printf("%s 𝐢 CL2 protocol detected", c.GiveName())
				c.Protocol = p
				resultCh <- p
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
		return p, true
	}

	// No valid protocol detected
	log.Println("No valid protocol detected")
	return nil, false
}
