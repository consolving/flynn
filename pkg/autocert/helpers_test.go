package autocert

import (
	"net/http"
	"net/http/httptest"
)

type fakeResponseWriter struct {
	status int
	header http.Header
	body   []byte
}

func (w *fakeResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *fakeResponseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	w.body = append(w.body, b...)
	return len(b), nil
}

func (w *fakeResponseWriter) WriteHeader(code int) {
	w.status = code
}

func newRequest(method, path string) *http.Request {
	return httptest.NewRequest(method, path, nil)
}
