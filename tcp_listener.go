package spoon

import (
	"crypto/tls"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

// TCPListener is spoon's Listener interface with extra helper methods.
type TCPListener interface {
	net.Listener
	SetKeepAlive(d time.Duration)
	SetForceTimeout(d time.Duration)
	ListenAndServe(addr string, handler http.Handler) error
	ListenAndServeTLS(addr string, certFile string, keyFile string, handler http.Handler) error
	RunServer(server *http.Server) error
}

func newtcpListener(l *net.TCPListener) *tcpListener {
	return &tcpListener{
		TCPListener:          l,
		keepaliveDuration:    3 * time.Minute,
		forceTimeoutDuration: 5 * time.Minute,
		wg:                   new(sync.WaitGroup),
		conns:                map[*net.TCPConn]*net.TCPConn{},
		m:                    new(sync.Mutex),
	}
}

type tcpListener struct {
	*net.TCPListener
	keepaliveDuration    time.Duration
	forceTimeoutDuration time.Duration
	wg                   *sync.WaitGroup
	conns                map[*net.TCPConn]*net.TCPConn
	m                    *sync.Mutex
}

var _ net.Listener = new(tcpListener)
var _ TCPListener = new(tcpListener)

func (l *tcpListener) Accept() (net.Conn, error) {

	conn, err := l.TCPListener.AcceptTCP()
	if err != nil {
		return nil, err
	}

	conn.SetKeepAlive(true)                      // see http.tcpKeepAliveListener
	conn.SetKeepAlivePeriod(l.keepaliveDuration) // see http.tcpKeepAliveListener

	zconn := zeroTCPConn{
		TCPConn: conn,
		wg:      l.wg,
		l:       l,
	}

	l.m.Lock()
	l.conns[conn] = conn
	l.m.Unlock()

	l.wg.Add(1)

	return zconn, nil
}

// blocking wait for close
func (l *tcpListener) Close() error {

	log.Println("Closing Listener:", l.Addr())

	//stop accepting connections - release fd
	err := l.TCPListener.Close()
	c := make(chan struct{})

	go func() {
		l.m.Lock()
		for _, v := range l.conns {
			log.Println("Closing Keepalives")

			// ok this needs some explanation
			v.Close() // this is OK to close, see (*TCPConn) SetLinger, just can't reduce waitgroup until it's actually closed!
		}
		l.m.Unlock()
	}()

	go func() {
		l.wg.Wait()
		close(c)
	}()

	select {
	case <-c:
	// closed gracefully
	case <-time.After(l.forceTimeoutDuration):
		log.Println("timeout reached, force shutdown")
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
	// net.Conn
	*net.TCPConn
	wg *sync.WaitGroup
	l  *tcpListener
}

func (conn zeroTCPConn) Close() (err error) {

	log.Println("Calling Close on Connection")

	if err = conn.TCPConn.Close(); err != nil {
		log.Println("ERROR CLOSING CONNECTION, OK if connection already closed, we must have triggered a restart: ", err)
	}

	conn.l.m.Lock()
	delete(conn.l.conns, conn.TCPConn)
	conn.l.m.Unlock()

	conn.wg.Done()
	return
}

//
// HTTP Section for tcpListener
//

// ListenAndServe mimics the std libraries http.ListenAndServe but uses our custom listener
// for graceful restarts.
// NOTE: addr is ignored, the address of the listener is used, only reason it is a param is for
// easier conversion from stdlib http.ListenAndServe
func (l *tcpListener) ListenAndServe(addr string, handler http.Handler) error {

	server := &http.Server{Addr: l.Addr().String(), Handler: handler}

	go server.Serve(l)
	return nil
}

// ListenAndServeTLS mimics the std libraries http.ListenAndServeTLS but uses out custom listener
// for graceful restarts.
// NOTE: addr is ignored, the address of the listener is used, only reason it is a param is for
// easier conversion from stdlib http.ListenAndServeTLS
func (l *tcpListener) ListenAndServeTLS(addr string, certFile string, keyFile string, handler http.Handler) error {

	var err error

	tlsConfig := &tls.Config{
		NextProtos:   []string{"http/1.1"},
		Certificates: make([]tls.Certificate, 1),
	}

	tlsConfig.Certificates[0], err = tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return err
	}

	tlsListener := tls.NewListener(l, tlsConfig)

	server := &http.Server{Addr: tlsListener.Addr().String(), Handler: handler, TLSConfig: tlsConfig}

	go server.Serve(tlsListener)

	return nil
}

// RunServer runs the provided http.Server, if using TLS, TLSConfig must be setup prior to
// calling this function
func (l *tcpListener) RunServer(server *http.Server) error {

	var lis net.Listener

	if server.TLSConfig != nil {
		if server.TLSConfig.NextProtos == nil {
			server.TLSConfig.NextProtos = make([]string, 0)
		}

		if len(server.TLSConfig.NextProtos) == 0 || !strSliceContains(server.TLSConfig.NextProtos, "http/1.1") {
			server.TLSConfig.NextProtos = append(server.TLSConfig.NextProtos, "http/1.1")
		}

		lis = tls.NewListener(l, server.TLSConfig)
	} else {
		lis = l
	}

	go server.Serve(lis)

	return nil
}

// SetKeepAlive sets the listener's connection keep alive timeout.
// NOTE: method is NOT thread safe, must set prior to sp.Run()
// DEFAULT: time.Minute * 3
func (l *tcpListener) SetKeepAlive(d time.Duration) {
	l.keepaliveDuration = d
}

// SetKeepAlive sets the listener's connection keep alive timeout.
// NOTE: method is NOT thread safe, must set prior to sp.Run()
// DEFAULT: time.Minute * 5
func (l *tcpListener) SetForceTimeout(d time.Duration) {
	l.forceTimeoutDuration = d
}

func strSliceContains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
