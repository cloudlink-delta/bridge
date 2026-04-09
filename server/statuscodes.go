package server

import "fmt"

// StatusCode represents a structured CloudLink status code
type StatusCode struct {
	Type    string // "I" for Info, "E" for Error
	Code    int
	Message string
}

// String generates the formatted string expected by the CloudLink protocol (e.g., "I:100 | OK")
func (s StatusCode) String() string {
	return fmt.Sprintf("%s:%d | %s", s.Type, s.Code, s.Message)
}

// Predefined CloudLink Status Codes (Ported from CLPv4.1)
var (
	StatusTest            = StatusCode{"I", 0, "Test"}
	StatusEcho            = StatusCode{"I", 1, "Echo"}
	StatusOK              = StatusCode{"I", 100, "OK"}
	StatusSyntax          = StatusCode{"E", 101, "Syntax"}
	StatusDatatype        = StatusCode{"E", 102, "Datatype"}
	StatusIDNotFound      = StatusCode{"E", 103, "ID not found"}
	StatusIDNotSpecific   = StatusCode{"E", 104, "ID not specific enough"}
	StatusInternalError   = StatusCode{"E", 105, "Internal server error"}
	StatusEmptyPacket     = StatusCode{"E", 106, "Empty packet"}
	StatusIDAlreadySet    = StatusCode{"E", 107, "ID already set"}
	StatusRefused         = StatusCode{"E", 108, "Refused"}
	StatusInvalidCommand  = StatusCode{"E", 109, "Invalid command"}
	StatusDisabledCommand = StatusCode{"E", 110, "Command disabled"}
	StatusIDRequired      = StatusCode{"E", 111, "ID required"}
	StatusIDConflict      = StatusCode{"E", 112, "ID conflict"}
	StatusTooLarge        = StatusCode{"E", 113, "Too large"}
	StatusJSONError       = StatusCode{"E", 114, "JSON error"}
	StatusRoomNotJoined   = StatusCode{"E", 115, "Room not joined"}
)
