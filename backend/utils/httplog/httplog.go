package httplog

import (
	"log"
	"net/http"
	"net/http/httputil"
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
		log.Printf("--> %s %s %s\n", r.Host, r.Method, r.URL)
	}

	resp, err := http.DefaultTransport.RoundTrip(r)

	if s.full {
		respBytes, _ := httputil.DumpResponse(resp, true)
		buf = append(buf, respBytes...)
		log.Printf("<-> %s\n%s\n", r.URL.String(), buf)
	} else if resp != nil {
		log.Printf("<-- %d %s\n", resp.StatusCode, resp.Request.URL)
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
