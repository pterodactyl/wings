package server

// Mount represents a Server Mount.
type Mount struct {
	Target   string `json:"target"`
	Source   string `json:"source"`
	ReadOnly bool   `json:"read_only"`
}
