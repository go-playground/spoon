package spoon

import (
	"net"
	"net/http"
)

// Server contains information about each separate address and port
// registered to spoon
type Server struct {
	server   *http.Server
	listener net.Listener
	handler  http.Handler
}
