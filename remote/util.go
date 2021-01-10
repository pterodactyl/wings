package remote

import (
	"net/http"

	"github.com/apex/log"
)

// Logs the request into the debug log with all of the important request bits.
// The authorization key will be cleaned up before being output.
//
// TODO(schrej): Somehow only execute the logic when log level is debug.
func debugLogRequest(req *http.Request) {
	headers := make(map[string][]string)
	for k, v := range req.Header {
		if k != "Authorization" || len(v) == 0 {
			headers[k] = v
			continue
		}

		headers[k] = []string{v[0][0:15] + "(redacted)"}
	}

	log.WithFields(log.Fields{
		"method":   req.Method,
		"endpoint": req.URL.String(),
		"headers":  headers,
	}).Debug("making request to external HTTP endpoint")
}
