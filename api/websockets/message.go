package websockets

type MessageType string

const (
	MessageTypeProc    MessageType = "proc"
	MessageTypeConsole MessageType = "console"
	MessageTypeStatus  MessageType = "status"
)

// Message is a message that can be sent using a websocket in JSON format
type Message struct {
	// Type is the type of a websocket message
	Type MessageType `json:"type"`
	// Payload is the payload of the message
	// The payload needs to support encoding in JSON
	Payload interface{} `json:"payload"`
}

type ProcPayload struct {
	Memory   int   `json:"memory"`
	CPUCores []int `json:"cpu_cores"`
	CPUTotal int   `json:"cpu_total"`
	Disk     int   `json:"disk"`
}

type ConsoleSource string
type ConsoleLevel string

const (
	ConsoleSourceWings  ConsoleSource = "wings"
	ConsoleSourceServer ConsoleSource = "server"

	ConsoleLevelPlain ConsoleLevel = "plain"
	ConsoleLevelInfo  ConsoleLevel = "info"
	ConsoleLevelWarn  ConsoleLevel = "warn"
	ConsoleLevelError ConsoleLevel = "error"
)

type ConsolePayload struct {
	// Source is the source of the console line, either ConsoleSourceWings or ConsoleSourceServer
	Source ConsoleSource `json:"source"`
	// Level is the level of the message.
	// Use one of plain, info, warn or error. If omitted the default is plain.
	Level ConsoleLevel `json:"level,omitempty"`
	// Line is the actual line to print to the console.
	Line string `json:"line"`
}

func (h *Hub) Log(l ConsoleLevel, m string) {
	h.Broadcast <- Message{
		Type: MessageTypeConsole,
		Payload: ConsolePayload{
			Source: ConsoleSourceWings,
			Level:  l,
			Line:   m,
		},
	}
}
