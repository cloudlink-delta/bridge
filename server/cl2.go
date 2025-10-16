package cloudlink

import (
	"log"
	"regexp"
	"strings"

	"github.com/goccy/go-json"
)

var cl2_parsers map[string]*regexp.Regexp

func init() {
	cl2_parsers = make(map[string]*regexp.Regexp)

	// Using named capture groups to easily extract parameters
	cl2_parsers["set_username"] = regexp.MustCompile(`^<%sn>\n(?P<Sender>.*)$`)
	cl2_parsers["disconnect"] = regexp.MustCompile(`^<%ds>\n(?P<Sender>.*)$`)
	cl2_parsers["simple_cmd"] = regexp.MustCompile(`^<%(rf|sh|rt)>\n?(?P<Sender>.*)$`)
	cl2_parsers["global_stream"] = regexp.MustCompile(`^<%gs>\n(?P<Sender>.*?)\n(?s)(?P<Data>.*)$`)
	cl2_parsers["private_stream"] = regexp.MustCompile(`^<%ps>\n(?P<Sender>.*?)\n(?P<Recipient>.*?)\n(?s)(?P<Data>.*)$`)
	cl2_parsers["linked_data"] = regexp.MustCompile(`^<%(l_g|l_p)>\n(?P<Mode>0)\n(?P<Sender>.*?)\n(?P<Recipient>.*?)\n(?s)(?P<Data>.*)$`)
	cl2_parsers["linked_vars"] = regexp.MustCompile(`^<%(l_g|l_p)>\n(?P<Mode>[1-2])\n(?P<Sender>.*?)\n(?P<RecipientVar>.*?)\n(?P<DataVar>.*?)\n(?s)(?P<Data>.*)$`)
}

// This structure is an abstract representation of a CL2 packet.
type CL2Packet struct {
	Command   string `json:"cmd,omitempty"`
	Mode      string `json:"mode,omitempty"`
	Sender    string `json:"sender,omitempty"`
	Recipient string `json:"recipient,omitempty"`
	Var       string `json:"var,omitempty"`
	Type      string `json:"type,omitempty"`
	Data      any    `json:"data,omitempty"`
	ID        string `json:"id,omitempty"`
}

type CL2Packet_TxReply struct {
	Type string           `json:"type"`
	Data CL2Packet_TxData `json:"data"`
	ID   string           `json:"id,omitempty"`
}

type CL2Packet_TxData struct {
	Type string `json:"type,omitempty"`
	Mode string `json:"mode,omitempty"`
	Var  string `json:"var,omitempty"`
	Data string `json:"data"`
}

func (c *CL2Packet) Bytes() []byte {
	b, err := json.Marshal(c)
	if err != nil {
		log.Println(err)
		return nil
	}
	return b
}

func (c *CL2Packet) String() string {
	// stub
	return string(c.Bytes())
}

func (c *CL2Packet) DeriveProtocol() uint {
	return Protocol_CL2
}

func (c *CL2Packet) DeriveDialect(_ *Client) uint {
	return Dialect_Undefined
}

func (c *CL2Packet) IsJSON() bool {
	return false
}

// Takes a raw byte array and returns true if it is a semistructured CL2 packet.
// Also populates the provided packet struct with the extracted parameters.
func (h *CL2Packet) Reader(raw []byte) bool {
	message := string(raw)
	for key, re := range cl2_parsers {
		if re.MatchString(message) {
			matches := re.FindStringSubmatch(message)
			names := re.SubexpNames()

			// Populate packet struct from named capture groups
			for i, name := range names {
				if i > 0 && i < len(matches) {
					switch name {
					case "Sender":
						h.Sender = matches[i]
					case "Recipient":
						h.Recipient = matches[i]
					case "Data":
						h.Data = matches[i]
					case "Mode":
						h.Mode = matches[i]
					case "RecipientVar":
						if h.Mode == "1" {
							h.Recipient = matches[i]
						} else {
							h.Var = matches[i]
						}
					case "DataVar":
						if h.Mode == "1" {
							h.Var = matches[i]
						}
					}
				}
			}

			// Extract the command
			if key == "simple_cmd" || key == "linked_data" || key == "linked_vars" {
				h.Command = re.FindStringSubmatch(message)[1]
			} else {
				h.Command = strings.Split(key, "_")[0]
			}
			return true
		}
	}
	return false // No match found
}

func (p *CL2Packet) Handler(c *Client, m *Manager) {
	switch p.Command {
	case "sh":
		if c.Handshake {
			return
		}
		c.JoinRoom(m.DefaultRoom)
		p.SendHandshake(c, m)
		c.UpdateHandshake(true)
	case "rf":
	case "set":
		c.SetName(p.Sender)
	case "global":
	case "private":
	case "disconnect":
	default:
		log.Println("Unknown CL2 Method")
	}
}

func (p *CL2Packet) SendHandshake(c *Client, m *Manager) {
	resp := &CL2Packet{
		Type: "direct",
		Data: &CL2Packet_TxData{
			Type: "vers",
			Data: m.ServerVersion,
		},
	}
	c.writer <- resp.Bytes()
}
