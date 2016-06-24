package spoon

import (
	"net"
	"os"
	"sync"
	"time"
)

func newZeroUnixListenerListener(l net.UnixListener, shutdownComplete chan struct{}, keepaliveDuration time.Duration) *zeroUnixListenerListener {
	return &zeroUnixListenerListener{
		UnixListener:      l,
		keepaliveDuration: keepaliveDuration,
		shutdownComplete:  shutdownComplete,
		wg:                new(sync.WaitGroup),
	}
}

type zeroUnixListenerListener struct {
	net.UnixListener
	keepaliveDuration time.Duration
	shutdownComplete  chan struct{}
	wg                *sync.WaitGroup
	closeError        error
}

var _ net.Listener = new(zeroUnixListenerListener)

func (l *zeroUnixListenerListener) Accept() (net.Conn, error) {

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
func (l *zeroUnixListenerListener) Close() error {

	//stop accepting connections - release fd
	l.closeError = l.UnixListener.Close()

	l.wg.Wait()

	l.shutdownComplete <- struct{}{}

	return l.closeError
}

func (l *zeroUnixListenerListener) File() *os.File {

	// returns a dup(2) - FD_CLOEXEC flag *not* set
	tl := l.UnixListener
	fl, _ := tl.File()

	return fl
}

//notifying on close net.Conn
type zeroUinxConn struct {
	net.Conn
	wg *sync.WaitGroup
}

func (conn zeroUinxConn) Close() (err error) {

	if err = conn.Conn.Close(); err != nil {
		return
	}

	conn.wg.Done()
	return
}
