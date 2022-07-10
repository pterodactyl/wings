package main

import (
	"encoding/gob"
	"github.com/pterodactyl/wings/cmd"
	"github.com/pterodactyl/wings/sftp"
	"math/rand"
	"time"
)

func main() {
	gob.Register(sftp.EventRecord{})

	// Since we make use of the math/rand package in the code, especially for generating
	// non-cryptographically secure random strings we need to seed the RNG. Just make use
	// of the current time for this.
	rand.Seed(time.Now().UnixNano())

	// Execute the main binary code.
	cmd.Execute()
}
