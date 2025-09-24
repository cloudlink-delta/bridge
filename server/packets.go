package cloudlink

import (
	"fmt"
	"log"

	"github.com/bwmarrin/snowflake"
	"github.com/goccy/go-json"
	"github.com/gofiber/contrib/websocket"
)

// MulticastMessage broadcasts a payload to multiple clients.
func MulticastMessage(clients map[snowflake.ID]*Client, message []byte) {
	for _, client := range clients {
		UnicastMessage(client, message)
	}
}

// UnicastMessageAny broadcasts a payload to a singular client.
func UnicastMessage(client *Client, message []byte) {
	if err := client.connection.WriteMessage(websocket.TextMessage, message); err != nil {
		log.Printf("Client %s (%s) send error: %s", client.id, client.uuid, err)
	}
}

// This structure represents the JSON formatting used for the current CloudLink formatting scheme.
// Values that are not specific to one type are represented with any.
type Packet_UPL struct {
	Cmd      string      `json:"cmd"`
	Name     any         `json:"name,omitempty"`
	Val      any         `json:"val,omitempty"`
	ID       any         `json:"id,omitempty"`
	Rooms    any         `json:"rooms,omitempty"`
	Listener any         `json:"listener,omitempty"`
	Code     string      `json:"code,omitempty"`
	CodeID   int         `json:"code_id,omitempty"`
	Mode     string      `json:"mode,omitempty"`
	Origin   *UserObject `json:"origin,omitempty"`
	Details  string      `json:"details,omitempty"`
}

func (packet Packet_UPL) ToBytes() []byte {
	marshaled, _ := json.Marshal(packet)
	return marshaled
}

func (packet Packet_UPL) String() string {
	return fmt.Sprintf("cmd: %s, name: %v, val: %v, id: %v, rooms: %v, listener: %v, code: %v, code_id: %v, mode: %v, origin: %v, details: %v", packet.Cmd, packet.Name, packet.Val, packet.ID, packet.Rooms, packet.Listener, packet.Code, packet.CodeID, packet.Mode, packet.Origin, packet.Details)
}

// This structure represents the JSON formatting the Packet_CloudVarScratch cloud variable protocol uses.
// Values that are not specific to one type are represented with any.
type Packet_CloudVarScratch struct {
	Method    string `json:"method"`
	ProjectID any    `json:"project_id,omitempty"`
	Username  string `json:"user,omitempty"`
	Value     any    `json:"value"`
	Name      any    `json:"name,omitempty"`
	NewName   any    `json:"new_name,omitempty"`
}

func (packet Packet_CloudVarScratch) ToBytes() []byte {
	marshaled, _ := json.Marshal(packet)
	return marshaled
}

// This structure is an abstract representation of a CL2 packet.
type Packet_CL2_RxPacket struct {
	Command   string
	Mode      string
	Sender    string
	Recipient string
	VarName   string
	Data      string
}

type Packet_CL2_TxSimple struct {
	Type string `json:"type"`
	Data string `json:"data"`
	ID   string `json:"id,omitempty"`
}

func (packet Packet_CL2_TxSimple) ToBytes() []byte {
	marshaled, _ := json.Marshal(packet)
	return marshaled
}

type Packet_CL2_TxData struct {
	Type string `json:"type,omitempty"`
	Mode string `json:"mode,omitempty"`
	Var  string `json:"var,omitempty"`
	Data string `json:"data"`
}

func (packet Packet_CL2_TxData) ToBytes() []byte {
	marshaled, _ := json.Marshal(packet)
	return marshaled
}

type Packet_CL2_TxReply struct {
	Type string            `json:"type"`
	Data Packet_CL2_TxData `json:"data"`
	ID   string            `json:"id,omitempty"`
}

func (packet Packet_CL2_TxReply) ToBytes() []byte {
	marshaled, _ := json.Marshal(packet)
	return marshaled
}
