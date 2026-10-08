package filesystem

import (
	"strconv"
	"strings"
	"testing"
	"time"

	. "github.com/franela/goblin"
)

func TestIgnore(t *testing.T) {
	g := Goblin(t)

	g.Describe("ValidateIgnore", func() {
		g.It("accepts a typical pattern list", func() {
			g.Assert(ValidateIgnore("")).IsNil()
			g.Assert(ValidateIgnore("# comment\n\nlogs/\n*.log\n**/cache/**\n!important.log\nworld (copy)/\r\n")).IsNil()
		})

		g.It("rejects a list larger than the maximum length", func() {
			g.Assert(ValidateIgnore(strings.Repeat("a", MaxIgnoreLength))).IsNil()
			g.Assert(ValidateIgnore(strings.Repeat("a", MaxIgnoreLength+1))).IsNotNil()
		})

		g.It("rejects a list with too many patterns", func() {
			lines := make([]string, 0, MaxIgnorePatterns+1)
			for i := 0; i < MaxIgnorePatterns; i++ {
				lines = append(lines, "file")
			}
			g.Assert(ValidateIgnore(strings.Join(lines, "\n"))).IsNil()
			lines = append(lines, "file")
			g.Assert(ValidateIgnore(strings.Join(lines, "\n"))).IsNotNil()
		})

		g.It("does not count blank lines or comments as patterns", func() {
			lines := make([]string, 0, MaxIgnorePatterns*3)
			for i := 0; i < MaxIgnorePatterns; i++ {
				lines = append(lines, "file", "", "# comment")
			}
			g.Assert(ValidateIgnore(strings.Join(lines, "\n"))).IsNil()
		})

		g.It("rejects a pattern with too many wildcards", func() {
			g.Assert(ValidateIgnore(strings.Repeat("*a", MaxIgnorePatternWildcards))).IsNil()
			g.Assert(ValidateIgnore(strings.Repeat("*a", MaxIgnorePatternWildcards+1))).IsNotNil()
		})

		g.It("does not count escaped wildcards", func() {
			g.Assert(ValidateIgnore(strings.Repeat(`\*a`, MaxIgnorePatternWildcards+1))).IsNil()
		})
	})

	g.Describe("escapeIgnorePattern", func() {
		g.It("escapes regular expression metacharacters that gitignore treats literally", func() {
			escaped, wildcards := escapeIgnorePattern("world (copy)/{a|b}+")
			g.Assert(escaped).Equal(`world \(copy\)/\{a\|b\}\+`)
			g.Assert(wildcards).Equal(0)
		})

		g.It("preserves gitignore escapes and character classes", func() {
			escaped, wildcards := escapeIgnorePattern(`\#not-a-comment\*[^a-z]*.log`)
			g.Assert(escaped).Equal(`\#not-a-comment\*[^a-z]*.log`)
			g.Assert(wildcards).Equal(1)
		})
	})

	g.Describe("compileIgnore", func() {
		g.It("matches paths containing metacharacters literally", func() {
			i, err := compileIgnore("world (copy)/\nc++/\n*.log\n!keep.log\n")
			g.Assert(err).IsNil()
			g.Assert(i.MatchesPath("world (copy)/level.dat")).IsTrue()
			g.Assert(i.MatchesPath("world copy/level.dat")).IsFalse()
			g.Assert(i.MatchesPath("c++/main.cpp")).IsTrue()
			g.Assert(i.MatchesPath("logs/latest.log")).IsTrue()
			g.Assert(i.MatchesPath("logs/keep.log")).IsFalse()
			g.Assert(i.MatchesPath("server.jar")).IsFalse()
		})

		g.It("does not expand repetition counts", func() {
			i, err := compileIgnore("(a{100}){10}\n")
			g.Assert(err).IsNil()
			g.Assert(i.MatchesPath(strings.Repeat("a", 1000))).IsFalse()
			g.Assert(i.MatchesPath("(a{100}){10}")).IsTrue()
		})

		g.It("returns an error for an invalid pattern list", func() {
			_, err := compileIgnore(strings.Repeat("*a", MaxIgnorePatternWildcards+1))
			g.Assert(err).IsNotNil()
		})
	})

	g.Describe("ignoreMatchBudget", func() {
		g.It("allows the base budget regardless of the file count", func() {
			b := &ignoreMatchBudget{base: 10 * time.Millisecond, perFile: time.Millisecond}
			g.Assert(b.track(9 * time.Millisecond)).IsNil()
			g.Assert(b.track(2 * time.Millisecond)).IsNotNil()
		})

		g.It("scales the budget with the number of files evaluated", func() {
			b := &ignoreMatchBudget{base: 10 * time.Millisecond, perFile: time.Millisecond}
			for n := 0; n < 100; n++ {
				g.Assert(b.track(900 * time.Microsecond)).IsNil()
			}
			g.Assert(b.track(20 * time.Millisecond)).IsNotNil()
		})

		g.It("reports the figures in the error", func() {
			b := &ignoreMatchBudget{base: time.Millisecond, perFile: time.Millisecond}
			err := b.track(5 * time.Millisecond)
			g.Assert(err).IsNotNil()
			g.Assert(strings.Contains(err.Error(), "5ms spent over 1 files (5ms per file)")).IsTrue()
		})

		// The budget for each file is exceeded by the most expensive list allowed, while
		// leaving plenty of room for an ordinary one.
		g.It("is exceeded by the most expensive list allowed but not by a typical one", func() {
			cost := func(lines []string) time.Duration {
				i, err := compileIgnore(strings.Join(lines, "\n"))
				g.Assert(err).IsNil()
				start := time.Now()
				for n := 0; n < 20; n++ {
					i.MatchesPath("aaaaaaaaaaaaaaaaaaaa/aaaaaaaaaaaaaaaaaaaa")
				}
				return time.Since(start) / 20
			}

			worst := make([]string, MaxIgnorePatterns)
			for n := range worst {
				worst[n] = "*" + strings.Repeat("a*", MaxIgnorePatternWildcards-1) + strconv.Itoa(n)
			}
			worstCost := cost(worst)
			if budget := newIgnoreMatchBudget(len(worst)).perFile; worstCost <= budget {
				g.Failf("the most expensive list costs %s per file, within the budget of %s", worstCost, budget)
			}

			// The longest list of ordinary patterns allowed must fit within its budget. The
			// race detector slows matching down by far more than the budget allows for.
			if !raceEnabled {
				ordinary := make([]string, MaxIgnorePatterns)
				for n := range ordinary {
					ordinary[n] = []string{"dir" + strconv.Itoa(n) + "/", "*.ext" + strconv.Itoa(n), "plugins/*/cache" + strconv.Itoa(n) + "/"}[n%3]
				}
				i, err := compileIgnore(strings.Join(ordinary, "\n"))
				g.Assert(err).IsNil()
				start := time.Now()
				for n := 0; n < 20; n++ {
					i.MatchesPath("plugins/Essentials/userdata/0123456789abcdef.yml")
				}
				if c, budget := time.Since(start)/20, newIgnoreMatchBudget(len(ordinary)).perFile; c > budget {
					g.Failf("the longest ordinary list costs %s per file, over its budget of %s", c, budget)
				}
			}

			// Compared against the most expensive list rather than the budget itself,
			// since the race detector slows down both by a large amount.
			typical := []string{"*.log", "logs/", "cache/", "*.tmp", "world/region/*.mca", "plugins/*/data/", "!plugins/keep/data/"}
			if c := cost(typical); c*50 > worstCost {
				g.Failf("a typical list costs %s per file, compared to %s for the most expensive list", c, worstCost)
			}
		})
	})
}
