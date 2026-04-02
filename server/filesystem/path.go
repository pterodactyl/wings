package filesystem

import (
	"emperror.dev/errors"
)

// IsIgnored checks if the given file or path is in the server's file denylist. If so, an Error
// is returned, otherwise nil is returned.
func (fs *Filesystem) IsIgnored(paths ...string) error {
	for _, p := range paths {
		if fs.denylist.MatchesPath(p) {
			return errors.WithStack(&Error{code: ErrCodeDenylistFile, path: p})
		}
	}
	return nil
}
