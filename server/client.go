package server

import (
	"fmt"
	"sync"
	"time"

	"github.com/gofiber/contrib/v3/websocket"
)

// Writer listens to the Sender channel and writes messages to the WebSocket.
// It ensures only one goroutine ever writes to the connection at a time.
func (c *BridgeClient) Writer() {
	defer c.Conn.Close()
	for {
		select {
		case msg, ok := <-c.writer:
			if !ok {
				return
			}
			if write_err := c.Conn.WriteMessage(websocket.TextMessage, msg); write_err != nil {
				c.Server.Logger.Error().Msgf("%s ⚠️  Error writing to client: %v", c.GiveName(), write_err)
			}
		case <-c.exit:
			return // Stop the goroutine
		}
	}
}

func (c *BridgeClient) Reader() {
	// Set a hard limit of 64KB
	c.Conn.SetReadLimit(64 * 1024)
reader:
	for {
		if msg_type, packet, err := c.Conn.ReadMessage(); err != nil {
			c.Server.Logger.Error().AnErr("error", err).Msg("Error reading from client")
			c.exit <- true
			break reader
		} else {

			// Rate limit check
			if c.Server.Config.Enable_Rate_Limit {

				now := time.Now()
				if c.last_msg_time.IsZero() {
					c.last_msg_time = now
				}
				if now.Sub(c.last_msg_time) > c.Server.Config.Rate_Limit_Interval {
					c.msg_count = 0
					c.last_msg_time = now
				}

				c.msg_count++

				exceeded := c.msg_count > c.Server.Config.Rate_Limit_Burst

				if exceeded {
					if c.Server.Config.Kick_On_Rate_Limit {
						c.Server.Logger.Error().Msgf("%s ⚠️  Aborting connection to client: Exceeded ratelimit.", c.GiveName())
						c.writer <- []byte("Your client has exceeded the ratelimit allowed by the server. Please reduce the messages that you send.")
						c.Server.Respond_With_Code(c.Conn, Ratelimit_Exceeded)
						c.exit <- true
						break reader
					} else {
						c.Server.Logger.Warn().Msgf("%s ⚠️  Client exceeding rate limit...", c.GiveName())
						// Silently drop new packets
						continue
					}
				}
			}

			switch msg_type {
			case websocket.TextMessage:
				switch p := c.Protocol.(type) {
				case nil:
					if p, ok := c.DetectAndReadProtocol(packet); !ok {
						c.Server.Logger.Error().Msgf("%s ⚠️  Aborting connection to client: Failed to identify protocol.", c.GiveName())
						err_msg := []byte("Failed to detect your client's protocol. Please try again later.")
						c.Server.Respond_With_Message_And_Code(c.Conn, Protocol_Detection_Failure, err_msg)
						c.exit <- true
						break reader
					} else {
						c.Protocol = p
					}
				case *CL4_or_CL3:
					go p.Reader(c, packet)
				case *Scratch_Handler:
					go p.Reader(c, packet)
				case *CL2:
					go p.Reader(c, packet)
				default:
					c.Server.Logger.Error().Msgf("%s ⚠️  Aborting connection to client: Failed to process client protocol.", c.GiveName())
					err_msg := []byte("Failed to process your client's protocol. Please report this to the server administrator.")
					c.Server.Respond_With_Message_And_Code(c.Conn, Protocol_Handler_Failure, err_msg)
					c.exit <- true
					break reader
				}

			default:
				c.Server.Logger.Error().Msgf("%s ⚠️  Aborting connection to client: Unsupported WebSocket frame type.", c.GiveName())
				err_msg := []byte("You sent a packet that the server does not understand; This server only supports text frames.")
				c.Server.Respond_With_Message_And_Code(c.Conn, Generic_Error, err_msg)
				c.exit <- true
				break reader
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

func (c *BridgeClient) GiveName() string {
	// If the username is nil or empty, return just the UUID
	if c.Username == nil || c.Username == "" {
		return fmt.Sprintf("[%s]", c.UUID)
	}
	// If they have a username, include it with the UUID
	return fmt.Sprintf("[%v (%s)]", c.Username, c.UUID)
}

func (c *BridgeClient) DetectAndReadProtocol(data []byte) (Protocol, bool) {
	var wg sync.WaitGroup
	resultCh := make(chan Protocol, 1) // Buffered channel of size 1

	detectors := []func(){
		func() {
			defer wg.Done()
			p := New_CL4_or_CL3(c.Server)
			if p.Reader(c, data) {
				c.Protocol = p
				resultCh <- p
			}
		},
		func() {
			defer wg.Done()
			p := New_Scratch(c.Server)
			if p.Reader(c, data) {
				c.Protocol = p
				resultCh <- p
			}
		},
		func() {
			defer wg.Done()
			p := New_CL2(c.Server)
			if p.Reader(c, data) {
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
	c.Server.Logger.Debug().Msgf("No valid protocol detected")
	return nil, false
}
