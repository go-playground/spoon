package spoon

import (
	"net"
	"os"
	"sync"
	"time"
)

func newGracefulListener(l net.Listener, shutdownComplete chan struct{}, keepaliveDuration time.Duration) *gracefulListener {
	return &gracefulListener{
		Listener:          l,
		keepaliveDuration: keepaliveDuration,
		shutdownComplete:  shutdownComplete,
		wg:                new(sync.WaitGroup),
	}
}

type gracefulListener struct {
	net.Listener
	keepaliveDuration time.Duration
	shutdownComplete  chan struct{}
	wg                *sync.WaitGroup
	closeError        error
}

var _ net.Listener = new(gracefulListener)

func (l *gracefulListener) Accept() (net.Conn, error) {

	conn, err := l.Listener.(*net.TCPListener).AcceptTCP()
	if err != nil {
		return nil, err
	}

	conn.SetKeepAlive(true)                      // see http.tcpKeepAliveListener
	conn.SetKeepAlivePeriod(l.keepaliveDuration) // see http.tcpKeepAliveListener

	gconn := gracefulConn{
		Conn: conn,
		wg:   l.wg,
		// closed: make(chan bool),
	}

	// why a good old os.Exit will fix that!
	// go func() {
	// 	//connection watcher
	// 	select {
	// 	case <-l.closeByForce:
	// 		uconn.Close()
	// 	case <-uconn.closed:
	// 		//closed manually
	// 	}
	// }()

	l.wg.Add(1)

	return gconn, nil
}

// //non-blocking trigger close
// func (l *gracefulListener) release(timeout time.Duration) {
// 	//stop accepting connections - release fd
// 	l.closeError = l.Listener.Close()

// 	//start timer, close by force if deadline not met
// 	waited := make(chan bool)
// 	go func() {
// 		l.wg.Wait()
// 		waited <- true
// 	}()
// 	go func() {
// 		select {
// 		case <-time.After(timeout):
// 			close(l.closeByForce)
// 		case <-waited:
// 			//no need to force close
// 		}
// 	}()
// }

// blocking wait for close
func (l *gracefulListener) Close() error {

	//stop accepting connections - release fd
	l.closeError = l.Listener.Close()

	l.wg.Wait()

	l.shutdownComplete <- struct{}{}

	return l.closeError
}

func (l *gracefulListener) File() *os.File {

	// returns a dup(2) - FD_CLOEXEC flag *not* set
	tl := l.Listener.(*net.TCPListener)
	fl, _ := tl.File()

	return fl
}

//notifying on close net.Conn
type gracefulConn struct {
	net.Conn
	wg *sync.WaitGroup
}

func (conn gracefulConn) Close() (err error) {

	if err = conn.Conn.Close(); err != nil {
		return
	}

	conn.wg.Done()
	return
}
