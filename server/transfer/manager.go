package transfer

import (
	"sync"
)

var (
	incomingTransfers = NewManager()
	outgoingTransfers = NewManager()
)

func Incoming() *Manager {
	return incomingTransfers
}

func Outgoing() *Manager {
	return outgoingTransfers
}

// Manager .
type Manager struct {
	mu        sync.RWMutex
	transfers map[string]*Transfer
}

// NewManager .
func NewManager() *Manager {
	return &Manager{
		transfers: make(map[string]*Transfer),
	}
}

// Add .
func (m *Manager) Add(transfer *Transfer) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.transfers[transfer.Server.ID()] = transfer
}

// Remove .
func (m *Manager) Remove(transfer *Transfer) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.transfers, transfer.Server.ID())
}

// Get .
func (m *Manager) Get(id string) *Transfer {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.transfers[id]
}
