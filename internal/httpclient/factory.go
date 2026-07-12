package httpclient

import (
	"net/http"
	"time"
)

// NewForExternal builds an outbound *http.Client with the given timeout.
// External integrations use direct connections; Tater Tube Server does not
// expose global proxy routing.
func NewForExternal(timeout time.Duration) *http.Client {
	return New(WithTimeout(timeout))
}
