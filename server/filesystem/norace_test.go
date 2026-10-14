//go:build !race

package filesystem

// raceEnabled reports whether the race detector is enabled, which slows down
// timing sensitive tests.
const raceEnabled = false
