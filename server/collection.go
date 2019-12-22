package server

import "sync"

type Collection struct {
	items []*Server
	mutex *sync.Mutex
}

// Create a new collection from a slice of servers.
func NewCollection(servers []*Server) *Collection {
	return &Collection{
		items: servers,
		mutex: &sync.Mutex{},
	}
}

// Return all of the items in the collection.
func (c *Collection) All() []*Server {
	return c.items
}

// Adds an item to the collection store.
func (c *Collection) Add(s *Server) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.items = append(c.items, s)
}

// Returns only those items matching the filter criteria.
func (c *Collection) Filter(filter func(*Server) bool) []*Server {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	r := make([]*Server, 0)
	for _, v := range c.items {
		if filter(v) {
			r = append(r, v)
		}
	}

	return r
}

// Returns a single element from the collection matching the filter. If nothing is
// found a nil result is returned.
func (c *Collection) Find(filter func(*Server) bool) *Server {
	for _, v := range c.items {
		if filter(v) {
			return v
		}
	}

	return nil
}

// Removes all items from the collection that match the filter function.
func (c *Collection) Remove(filter func(*Server) bool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	r := make([]*Server, 0)
	for _, v := range c.items {
		if !filter(v) {
			r = append(r, v)
		}
	}

	c.items = r
}
