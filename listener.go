package spoon

import "net"

// Listener is spoon's Listener interface with extra helper methods.
type Listener interface {
	net.Listener
}
