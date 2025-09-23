package cloudlink

// This structure represents the JSON formatting used for the current CloudLink formatting scheme.
// Values that are not specific to one type are represented with any.
type PacketUPL struct {
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

// This structure represents the JSON formatting the Scratch cloud variable protocol uses.
// Values that are not specific to one type are represented with any.
type Scratch struct {
	Method    string `json:"method"`
	ProjectID any    `json:"project_id,omitempty"`
	Username  string `json:"user,omitempty"`
	Value     any    `json:"value"`
	Name      any    `json:"name,omitempty"`
	NewName   any    `json:"new_name,omitempty"`
}

}
