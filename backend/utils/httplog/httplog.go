package httplog

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
)

/*
	simple http transport logger

this is a low level transport logger, therefore the output contains all your precious secrets.
this is a logger for development and debugging purposes uns should never be used in full mode in a
production envirionment
*/
type loggingTransport struct {
	fullRequest     bool
	fullResponse    bool
	logRequestBody  bool
	logResponseBody bool
}

func (s *loggingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	var buf []byte = []byte{}
	var err error

	if s.fullRequest {
		buf, err = httputil.DumpRequestOut(r, s.logRequestBody)
		if err != nil {
			buf = fmt.Appendf(buf, "error dumping request %s %s %s\n", r.Host, r.Method, r.URL)
		}
	} else {
		log.Printf("--> %s %s %s\n", r.Host, r.Method, r.URL)
	}

	resp, err := http.DefaultTransport.RoundTrip(r)
	// err is returned after dumping the response

	if s.fullResponse {
		if resp != nil {
			respBytes, derr := httputil.DumpResponse(resp, s.logResponseBody)
			if err != nil {
				buf = fmt.Appendf(buf, "error dumping response %s %s %s:\n%#v", r.Host, r.Method, r.URL, derr)
			} else {
				buf = append(buf, respBytes...)
			}
			log.Printf("<-> %s\n%s\n", r.URL.String(), buf)
		} else {
			buf = fmt.Appendf(buf, "error obtaining response %s %s %s:\n%#v", r.Host, r.Method, r.URL, err)
		}
	} else {
		if resp != nil {
			log.Printf("<-- %d %s\n", resp.StatusCode, resp.Request.URL)
		} else {
			log.Printf("<-- error obtaining response %s %s %s:\n%#v", r.Host, r.Method, r.URL, err)
		}
	}
	return resp, err
}

func NewSimpleLoggingTransport() *loggingTransport {
	return NewLoggingTransport(false, false, false, false)
}

func NewFullLoggingTransport(logBody bool) *loggingTransport {
	return NewLoggingTransport(true, true, logBody, logBody)
}

func NewLoggingTransport(logReq, logResp, logReqBody, logRespBody bool) *loggingTransport {
	return &loggingTransport{logReq, logResp, logReqBody, logRespBody}
}
