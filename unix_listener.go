package spoon

import (
	"log"
	"net"
	"os"
	"sync"
	"time"
)

func newUnixListener(l net.UnixListener) *unixListener {
	return &unixListener{
		UnixListener:         l,
		forceTimeoutDuration: time.Minute * 5,
	}
}

type unixListener struct {
	net.UnixListener
	forceTimeoutDuration time.Duration
	wg                   sync.WaitGroup
}

var _ net.Listener = new(unixListener)

func (l *unixListener) Accept() (net.Conn, error) {

	conn, err := l.UnixListener.AcceptUnix()
	if err != nil {
		return nil, err
	}

	zconn := zeroUinxConn{
		Conn: conn,
		wg:   l.wg,
	}

	l.wg.Add(1)

	return zconn, nil
}

// blocking wait for close
func (l *unixListener) Close() error {

	log.Println("Closing Listener:", l.Addr())

	//stop accepting connections - release fd
	err := l.UnixListener.Close()
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
		log.Println("timeout reached, force shutdown")
		// not waiting any longer, letting this go.
		// spoon will think it's been closed and when
		// the process dies, connections will get cut
	}

	return err
}

func (l *unixListener) File() *os.File {

	// returns a dup(2) - FD_CLOEXEC flag *not* set
	tl := l.UnixListener
	fl, _ := tl.File()

	return fl
}

//notifying on close net.Conn
type zeroUinxConn struct {
	net.Conn
	wg sync.WaitGroup
}

func (conn zeroUinxConn) Close() (err error) {

	if err = conn.Conn.Close(); err != nil {
		return
	}

	conn.wg.Done()
	return
}
