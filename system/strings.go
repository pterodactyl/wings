package system

import (
	"math/rand"
	"regexp"
	"strings"
)

var ipTrimRegex = regexp.MustCompile(`(:\d*)?$`)

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

// TrimIPSuffix removes the internal port value from an IP address to ensure we're only
// ever working directly with the IP address.
func TrimIPSuffix(s string) string {
	return ipTrimRegex.ReplaceAllString(s, "")
}
