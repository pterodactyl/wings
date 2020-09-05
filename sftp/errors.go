package sftp

type fxerr uint32

const (
	// Extends the default SFTP server to return a quota exceeded error to the client.
	//
	// @see https://tools.ietf.org/id/draft-ietf-secsh-filexfer-13.txt
	ErrSshQuotaExceeded = fxerr(15)
)

func (e fxerr) Error() string {
	switch e {
	case ErrSshQuotaExceeded:
		return "Quota Exceeded"
	default:
		return "Failure"
	}
}
