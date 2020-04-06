package websocket

const (
	AuthenticationSuccessEvent = "auth success"
	TokenExpiringEvent         = "token expiring"
	TokenExpiredEvent          = "token expired"
	AuthenticationEvent        = "auth"
	SetStateEvent              = "set state"
	SendServerLogsEvent        = "send logs"
	SendCommandEvent           = "send command"
	ErrorEvent                 = "daemon error"
)

type Message struct {
	// The event to perform. Should be one of the following that are supported:
	//
	// - status : Returns the server's power state.
	// - logs : Returns the server log data at the time of the request.
	// - power : Performs a power action aganist the server based the data.
	// - command : Performs a command on a server using the data field.
	Event string `json:"event"`

	// The data to pass along, only used by power/command currently. Other requests
	// should either omit the field or pass an empty value as it is ignored.
	Args []string `json:"args,omitempty"`
}
