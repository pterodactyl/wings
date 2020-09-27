package websocket

const (
	AuthenticationSuccessEvent = "auth success"
	TokenExpiringEvent         = "token expiring"
	TokenExpiredEvent          = "token expired"
	AuthenticationEvent        = "auth"
	SetStateEvent              = "set state"
	SendServerLogsEvent        = "send logs"
	SendCommandEvent           = "send command"
	SendStatsEvent             = "send stats"
	ErrorEvent                 = "daemon error"
	JwtErrorEvent              = "jwt error"
)

type Message struct {
	// The event to perform.
	Event string `json:"event"`

	// The data to pass along, only used by power/command currently. Other requests
	// should either omit the field or pass an empty value as it is ignored.
	Args []string `json:"args,omitempty"`
}
