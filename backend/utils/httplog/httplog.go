package httplog

import (
	"net/http"
	"net/http/httputil"

	"backend/logging"
)

type loggingTransport struct {
	full    bool
	logBody bool
}

func (s *loggingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	var buf []byte
	if s.full {
		buf, _ = httputil.DumpRequestOut(r, s.logBody)
	} else {
		logging.Debugf("--> %s %s %s\n", r.Host, r.Method, r.URL)
	}

	resp, err := http.DefaultTransport.RoundTrip(r)

	if s.full {
		respBytes, _ := httputil.DumpResponse(resp, true)
		buf = append(buf, respBytes...)
		logging.Debugf("<-> %s\n%s\n", r.URL.String(), buf)
	} else if resp != nil {
		logging.Debugf("<-- %d %s\n", resp.StatusCode, resp.Request.URL)
	}

	return resp, err
}

func filterAuthorization(headers http.Header) http.Header {
	filteredHeaders := make(http.Header)
	for k, v := range headers {
		if k == "Authorization" {
			filteredHeaders[k] = []string{"<removed>"}
		} else {
			filteredHeaders[k] = v
		}
	}
	return filteredHeaders
}
