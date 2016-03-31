package spoon

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

const (
	envListenerFDS        = "GO_LISTENER_FDS"
	envForceTerminationNS = "GO_LISTENER_FORCE_TERMINATE_NS"
	envKeepAliveNS        = "GO_LISTENER_KEEP_ALIVE_NS"
)

func (s *Spoon) getExtraParams(key string) string {
	return os.Getenv(key)
}

// IsSlaveProcess returns if the current process is the main master process or slave,
//
// helps is determining when to load up all your handler dependencies i.e.
// DB connections, caches...
func (s *Spoon) IsSlaveProcess() bool {
	return s.getExtraParams(envListenerFDS) != ""
}

func (s *Spoon) listenSetup(addr string) (net.Listener, error) {

	fds := s.getExtraParams(envListenerFDS)

	// first server, let's call it master, to get started
	if fds == "" { // if not slave
		err := s.setupFileDescriptors(addr)
		if err != nil {
			return nil, err
		}

		return nil, nil
	}

	forceTerm := s.getExtraParams(envForceTerminationNS)
	keepAlive := s.getExtraParams(envKeepAliveNS)

	if i, err := strconv.ParseInt(forceTerm, 10, 64); err == nil {
		s.SetForceTerminationTimeout(time.Duration(i))
	}

	if i, err := strconv.ParseInt(keepAlive, 10, 64); err == nil {
		s.SetKeepAliveDuration(time.Duration(i))
	}

	f := os.NewFile(uintptr(s.fdIndex), "")
	s.fdIndex++

	l, err := net.FileListener(f)
	if err != nil {
		fmt.Println(err)
		return nil, &FileDescriptorError{innerError: fmt.Errorf("failed to inherit file descriptor: %d error: %s", s.fdIndex, err)}
	}

	gListener := newGracefulListener(l, s.gracefulShutdownComplete, s.keepaliveDuration)

	return gListener, nil
}

// ListenAndServe mimics the std libraries http.ListenAndServe
func (s *Spoon) ListenAndServe(addr string, handler http.Handler) error {

	s.m.Lock()
	defer s.m.Unlock()

	gListener, err := s.listenSetup(addr)
	if err != nil {
		return err
	}

	if !s.IsSlaveProcess() {
		return nil
	}

	server := &http.Server{Addr: gListener.Addr().String(), Handler: handler}

	srv := &Server{
		server:   server,
		listener: gListener,
		handler:  handler,
	}

	s.servers = append(s.servers, srv)

	return nil
}

func (s *Spoon) signalParent() {
	time.Sleep(time.Second)
	pid := os.Getppid()

	proc, err := os.FindProcess(pid)
	if err != nil {
		s.errFunc(&SignalParentError{fmt.Errorf("ERROR FINDING MASTER PROCESS: %s", err)})
	}

	err = proc.Signal(syscall.SIGUSR1)
	if err != nil {
		s.errFunc(&SignalParentError{fmt.Errorf("ERROR SIGNALING MASTER PROC: %s", err)})
	}
}

// ListenAndServeTLS mimics the std libraries http.ListenAndServeTLS
func (s *Spoon) ListenAndServeTLS(addr string, certFile string, keyFile string, handler http.Handler) error {

	s.m.Lock()
	defer s.m.Unlock()

	gListener, err := s.listenSetup(addr)
	if err != nil {
		return err
	}

	if !s.IsSlaveProcess() {
		return nil
	}

	tlsConfig := &tls.Config{
		NextProtos:   []string{"http/1.1"},
		Certificates: make([]tls.Certificate, 1),
	}

	tlsConfig.Certificates[0], err = tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return err
	}

	tlsListener := tls.NewListener(gListener, tlsConfig)

	server := &http.Server{Addr: gListener.Addr().String(), Handler: handler, TLSConfig: tlsConfig}

	srv := &Server{
		server:   server,
		listener: tlsListener,
	}

	s.servers = append(s.servers, srv)

	return nil
}

// RunServer runs the provided server, if using TLS, TLSConfig must be setup prior to
// calling this function
func (s *Spoon) RunServer(server *http.Server) error {

	s.m.Lock()
	defer s.m.Unlock()

	gListener, err := s.listenSetup(server.Addr)
	if err != nil {
		return err
	}

	if !s.IsSlaveProcess() {
		return nil
	}

	if server.TLSConfig != nil {
		if server.TLSConfig.NextProtos == nil {
			server.TLSConfig.NextProtos = make([]string, 0)
		}

		if len(server.TLSConfig.NextProtos) == 0 || !strSliceContains(server.TLSConfig.NextProtos, "http/1.1") {
			server.TLSConfig.NextProtos = append(server.TLSConfig.NextProtos, "http/1.1")
		}

		gListener = tls.NewListener(gListener, server.TLSConfig)
	}

	srv := &Server{
		server:   server,
		listener: gListener,
	}

	s.servers = append(s.servers, srv)

	return nil
}

func strSliceContains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func (s *Spoon) startSlave() error {

	fds := envListenerFDS + "=" + strconv.Itoa(len(s.fileDescriptors))
	ft := envForceTerminationNS + "=" + strconv.FormatInt(s.forceTerminateTimeout.Nanoseconds(), 10)
	ka := envKeepAliveNS + "=" + strconv.FormatInt(s.keepaliveDuration.Nanoseconds(), 10)

	e := os.Environ()
	e = append(e, fds, ft, ka)

	// start server
	oldCmd := s.slave

	s.slave = exec.Command(s.binaryPath)
	s.slave.Env = e
	s.slave.Args = os.Args
	s.slave.Stdin = os.Stdin
	s.slave.Stdout = os.Stdout
	s.slave.Stderr = os.Stderr
	s.slave.ExtraFiles = s.fileDescriptors

	signals := make(chan os.Signal)
	signal.Notify(signals, syscall.SIGUSR1)

	go func() {

		// wait for slave to signal it is up and running
		<-signals // slave notifies master process that it's up and running

		s.logFunc("SIGUSR1 Recieved from slave")

		signal.Stop(signals)

		// will have to use current s.slave, if set, to trigger shutdown of old slave
		if oldCmd != nil {
			// shut down server! .. gracefully
			oldCmd.Process.Signal(syscall.SIGTERM)
		}

		cmdwait := make(chan error)
		go func() {
			// wait for slave process to finish, one way or another
			cmdwait <- s.slave.Wait()
		}()

		go func() {
			select {
			case err := <-cmdwait:
				if err != nil {
					s.errFunc(&SlaveShutdownError{innerError: err})
				}

				s.logFunc("Slave Shutdown Complete")
			}
		}()
	}()

	if err := s.slave.Start(); err != nil {
		return &SlaveStartError{innerError: err}
	}

	return nil
}

func (s *Spoon) setupFileDescriptors(addr string) error {

	if s.fileDescriptors == nil {
		s.fileDescriptors = make([]*os.File, 0)
	}

	a, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return &FileDescriptorError{innerError: fmt.Errorf("Invalid address %s (%s)", addr, err)}
	}

	l, err := net.ListenTCP("tcp", a)
	if err != nil {
		return &FileDescriptorError{innerError: err}
	}

	f, err := l.File()
	if err != nil {
		return &FileDescriptorError{innerError: fmt.Errorf("Failed to retreive fd for: %s (%s)", addr, err)}
	}

	if err := l.Close(); err != nil {
		return &FileDescriptorError{innerError: fmt.Errorf("Failed to close listener for: %s (%s)", addr, err)}
	}

	s.fileDescriptors = append(s.fileDescriptors, f)

	return nil
}

// Go starts up services + listeners and blocks until complete
func (s *Spoon) Go() error {

	s.m.Lock()
	defer s.m.Unlock()

	if s.IsSlaveProcess() {

		done := make(chan bool)

		for _, s := range s.servers {
			go s.server.Serve(s.listener)
		}

		// TODO: don't forget to setup Signal listening from syscall.SIGTERM to TRIGGER the restart
		signals := make(chan os.Signal)
		signal.Notify(signals, syscall.SIGTERM)

		go func() {
			<-signals

			s.logFunc(fmt.Sprint("TERMINATE SIGNAL RECEIVED", s.forceTerminateTimeout))

			closed := make(chan int)

			go func() {
				select {
				case <-s.gracefulShutdownComplete:
					s.logFunc("Gracefully shutdown")
					closed <- 0
				// this is most likely going to be hit if your app uses websockets
				case <-time.After(s.forceTerminateTimeout):
					s.logFunc("Force Shutdown!")
					closed <- 1
				}
			}()

			s.logFunc("Closing Slave Listener(s)")

			for _, s := range s.servers {
				go s.listener.Close()
			}

			os.Exit(<-closed)
		}()

		go s.signalParent()

		<-done

		return nil
	}

	err := s.startSlave()
	if err != nil {
		return err
	}

	go func() {
		for {
			<-s.gracefulRestartChannel

			s.logFunc("Graceful restart triggered")

			// graceful restart triggered
			err := s.startSlave()
			if err != nil {
				s.errFunc(&SlaveStartError{innerError: fmt.Errorf("ERROR starting new slave gracefully %s", err)})
			}
		}
	}()

	// wait for close signals here
	signals := make(chan os.Signal)
	signal.Notify(signals, syscall.SIGTERM)

	<-signals

	return nil
}
