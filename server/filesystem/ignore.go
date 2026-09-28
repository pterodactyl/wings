package filesystem

import (
	"fmt"
	"strings"
	"time"

	ignore "github.com/sabhiram/go-gitignore"
)

// Limits applied to the gitignore-style pattern lists used by an Archive, whether they
// come from the Panel with a backup request or from a server's .pteroignore file.
const (
	// MaxIgnoreLength is the maximum size, in bytes, of a pattern list.
	MaxIgnoreLength = 32 * 1024
	// MaxIgnorePatterns is the maximum number of non-blank, non-comment lines in a list.
	MaxIgnorePatterns = 256
	// MaxIgnorePatternWildcards is the maximum number of unescaped "*" characters in a pattern.
	MaxIgnorePatternWildcards = 16
)

// ValidateIgnore checks a pattern list against the limits above. The returned error
// describes the first limit exceeded and is safe to display to the user.
func ValidateIgnore(s string) error {
	_, err := parseIgnore(s)
	return err
}

// compileIgnore validates a pattern list and compiles it into a matcher.
func compileIgnore(s string) (*ignore.GitIgnore, error) {
	lines, err := parseIgnore(s)
	if err != nil {
		return nil, err
	}
	return ignore.CompileIgnoreLines(lines...), nil
}

// parseIgnore splits a pattern list into lines, enforces the limits above and escapes any
// regular expression metacharacters that gitignore treats literally so that go-gitignore
// does not interpret them.
func parseIgnore(s string) ([]string, error) {
	if len(s) > MaxIgnoreLength {
		return nil, fmt.Errorf("The ignored files list may not be larger than %d KiB.", MaxIgnoreLength/1024)
	}
	lines := strings.Split(s, "\n")
	var patterns int
	for n, line := range lines {
		if !isIgnorePattern(line) {
			continue
		}
		patterns++
		if patterns > MaxIgnorePatterns {
			return nil, fmt.Errorf("The ignored files list may not contain more than %d patterns.", MaxIgnorePatterns)
		}
		escaped, wildcards := escapeIgnorePattern(line)
		if wildcards > MaxIgnorePatternWildcards {
			return nil, fmt.Errorf("The ignored files pattern on line %d may not contain more than %d wildcards.", n+1, MaxIgnorePatternWildcards)
		}
		lines[n] = escaped
	}
	return lines, nil
}

// isIgnorePattern mirrors the checks go-gitignore performs before compiling a line so that
// blank lines and comments are not counted as patterns.
func isIgnorePattern(line string) bool {
	line = strings.TrimRight(line, "\r")
	if strings.HasPrefix(line, "#") {
		return false
	}
	return strings.Trim(line, " ") != ""
}

// escapeIgnorePattern escapes the regular expression metacharacters in a pattern that
// gitignore treats literally and returns the number of unescaped wildcards it contains.
func escapeIgnorePattern(line string) (string, int) {
	var (
		b         strings.Builder
		wildcards int
	)
	b.Grow(len(line) + 8)
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch c {
		case '\\':
			// Preserve gitignore escapes such as "\#", "\!" and "\*" as they are.
			b.WriteByte(c)
			if i+1 < len(line) {
				i++
				b.WriteByte(line[i])
			}
		case '*':
			wildcards++
			b.WriteByte(c)
		case '(', ')', '{', '}', '|', '+':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), wildcards
}

// The limits above bound the size of a pattern list but not the cost of evaluating it,
// which also grows with the length of every path in the server. Each archive may spend
// ignoreMatchBaseBudget matching patterns plus ignoreMatchPerFileBudget for every file
// evaluated, so a large server with an ordinary list never trips the budget: a typical
// list costs well under 0.1ms per file whereas a worst-case list costs several milliseconds.
const (
	ignoreMatchBaseBudget    = 5 * time.Second
	ignoreMatchPerFileBudget = 2 * time.Millisecond
)

// ignoreMatchBudget tracks the time an archive has spent evaluating its ignore patterns.
type ignoreMatchBudget struct {
	base    time.Duration
	perFile time.Duration
	spent   time.Duration
	files   int64
}

func newIgnoreMatchBudget() *ignoreMatchBudget {
	return &ignoreMatchBudget{base: ignoreMatchBaseBudget, perFile: ignoreMatchPerFileBudget}
}

// track records the time spent evaluating one more file and returns an error, including
// the figures for the logs, once the budget has been exceeded.
func (b *ignoreMatchBudget) track(d time.Duration) error {
	b.spent += d
	b.files++
	allowed := time.Duration(b.files) * b.perFile
	if allowed < b.base {
		allowed = b.base
	}
	if b.spent <= allowed {
		return nil
	}
	return fmt.Errorf(
		"filesystem: ignore pattern matching exceeded its time budget: %s spent over %d files (%s per file)",
		b.spent.Round(time.Millisecond), b.files, (b.spent / time.Duration(b.files)).Round(time.Microsecond),
	)
}
