package spoon

import (
	"log"
	"net"
	"os"
	"sync"
	"time"
)

func newtcpListener(l *net.TCPListener) *tcpListener {
	return &tcpListener{
		TCPListener:          l,
		keepaliveDuration:    3 * time.Minute,
		forceTimeoutDuration: 5 * time.Minute,
	}
}

type tcpListener struct {
	*net.TCPListener
	keepaliveDuration    time.Duration
	forceTimeoutDuration time.Duration
	wg                   sync.WaitGroup
}

var _ net.Listener = new(tcpListener)
var _ Listener = new(tcpListener)

func (l *tcpListener) Accept() (net.Conn, error) {

	conn, err := l.TCPListener.AcceptTCP()
	if err != nil {
		return nil, err
	}

	conn.SetKeepAlive(true)                      // see http.tcpKeepAliveListener
	conn.SetKeepAlivePeriod(l.keepaliveDuration) // see http.tcpKeepAliveListener

	zconn := zeroTCPConn{
		Conn: conn,
		wg:   l.wg,
	}

	l.wg.Add(1)

	return zconn, nil
}

// blocking wait for close
func (l *tcpListener) Close() error {

	//stop accepting connections - release fd
	err := l.TCPListener.Close()
	c := make(chan struct{})

	go func() {
		l.wg.Wait()
		close(c)
	}()

	select {
	case <-c:
	// closed gracefully
	case <-time.After(l.forceTimeoutDuration):
		l.SetDeadline(time.Now())
		// not waiting any longer, letting this go.
		// spoon will think it's been closed and when
		// the process dies, connections will get cut
	}

	return err
}

func (l *tcpListener) File() *os.File {

	// returns a dup(2) - FD_CLOEXEC flag *not* set
	tl := l.TCPListener
	fl, _ := tl.File()

	return fl
}

//notifying on close net.Conn
type zeroTCPConn struct {
	net.Conn
	wg sync.WaitGroup
}

func (conn zeroTCPConn) Close() (err error) {

	if err = conn.Conn.Close(); err != nil {
		log.Println("ERROR CLOSING CONNECTION:", err)
		return
	}

	conn.wg.Done()
	return
}
