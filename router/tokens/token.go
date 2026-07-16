package tokens

import (
	"strings"
)

type JwtScope string

const (
	Websocket      = JwtScope("websocket")
	FileUpload     = JwtScope("file-upload")
	FileDownload   = JwtScope("file-download")
	BackupDownload = JwtScope("backup-download")
	ServerTransfer = JwtScope("transfer")
)

type Scoped struct {
	Scope string `json:"scope"`
}

func (s Scoped) Scopes() []string {
	return strings.Split(s.Scope, " ")
}

func (s Scoped) HasScope(scope JwtScope) bool {
	for _, v := range s.Scopes() {
		if v == string(scope) {
			return true
		}
	}

	return false
}
