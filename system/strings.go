package system

import (
	"math/rand"
	"strings"
)

const characters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890"

// RandomString generates a random string of alpha-numeric characters using a
// pseudo-random number generator. The output of this function IS NOT cryptographically
// secure, it is used solely for generating random strings outside a security context.
func RandomString(n int) string {
	var b strings.Builder
	b.Grow(n)
	for i := 0; i < n; i++ {
		b.WriteByte(characters[rand.Intn(len(characters))])
	}
	return b.String()
}
