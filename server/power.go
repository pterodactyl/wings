package server

type PowerAction struct {
	Action string `json:"action"`
}

func (pr *PowerAction) IsValid() bool {
	return pr.Action == "start" ||
		pr.Action == "stop" ||
		pr.Action == "kill" ||
		pr.Action == "restart"
}
