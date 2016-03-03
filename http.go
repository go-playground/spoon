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

// Run sets up the addr listener File Descriptors and runs the application
func (s *Spoon) run(addr string) error {

	if s.IsSlaveProcess() {
		return nil
	}

	err := s.setupFileDescriptors(addr)
	if err != nil {
		return err
	}

	err = s.startSlave()
	if err != nil {
		return err
	}

	go func() {
		for {
			<-s.gracefulRestartChannel

			fmt.Println("Graceful restart triggered")
			// graceful restart triggered
			err := s.startSlave()
			if err != nil {
				fmt.Println("ERROR starting new slave gracefully", err)
			}
		}
	}()

	// wait for close signals here
	signals := make(chan os.Signal)
	signal.Notify(signals, syscall.SIGTERM)

	<-signals

	return nil
}

func (s *Spoon) listenSetup(addr string) (net.Listener, error) {

	fds := s.getExtraParams(envListenerFDS)

	// first server, let's call it master, to get started
	if fds == "" {
		return nil, s.run(addr)
	}

	forceTerm := s.getExtraParams(envForceTerminationNS)
	keepAlive := s.getExtraParams(envKeepAliveNS)

	if i, err := strconv.ParseInt(forceTerm, 10, 64); err == nil {
		s.SetForceTerminationTimeout(time.Duration(i))
	}

	if i, err := strconv.ParseInt(keepAlive, 10, 64); err == nil {
		s.SetKeepAliveDuration(time.Duration(i))
	}

	// is a slave process
	// do slave logic to get File Descriptors

	// numFDs, err := strconv.Atoi(s.getExtraParams(envListenerFDS))
	// if err != nil {
	// 	return fmt.Errorf("invalid %s integer", envListenerFDS)
	// }

	// for i := 0; i < numFDs; i++ {

	f := os.NewFile(uintptr(3), "")

	l, err := net.FileListener(f)
	if err != nil {
		return nil, fmt.Errorf("failed to inherit file descriptor: %d", 0)
	}
	// }

	gListener := newGracefulListener(l, s.gracefulShutdownComplete, s.keepaliveDuration)

	// TODO: don't forget to setup Signal listening from syscall.SIGTERM to TRIGGER the restart
	//
	signals := make(chan os.Signal)
	signal.Notify(signals, syscall.SIGTERM)

	go func() {
		<-signals

		fmt.Println("TERMINATE SIGNAL RECEIVED", s.forceTerminateTimeout)

		go func() {
			select {
			case <-s.gracefulShutdownComplete:
				fmt.Println("Gracefully shutdown")
				os.Exit(0)
			// this is most likely going to be hit if your app uses websockets
			case <-time.After(s.forceTerminateTimeout):
				fmt.Println("Force Shutdown!")
				os.Exit(1)
			}
		}()

		fmt.Println("Closing Slave Listener")

		gListener.Close()
	}()

	return gListener, nil
}

// ListenAndServe mimics the std libraries http.ListenAndServe
func (s *Spoon) ListenAndServe(addr string, handler http.Handler) error {

	gListener, err := s.listenSetup(addr)
	if err != nil {
		return err
	}

	go s.signalParent()

	return http.Serve(gListener, handler)
}

func (s *Spoon) signalParent() {
	time.Sleep(time.Second)
	pid := os.Getppid()

	proc, err := os.FindProcess(pid)
	if err != nil {
		er := fmt.Errorf("master process: %s", err)
		fmt.Println("ERROR FINDING MASTER PROCESS:", er)
	}

	err = proc.Signal(syscall.SIGUSR1)
	if err != nil {
		er := fmt.Errorf("signaling master process: %s", err)
		fmt.Println("ERROR SIGNALING MASTER PROC", er)
	}
}

// ListenAndServeTLS mimics the std libraries http.ListenAndServeTLS
func (s *Spoon) ListenAndServeTLS(addr string, certFile string, keyFile string, handler http.Handler) error {

	gListener, err := s.listenSetup(addr)
	if err != nil {
		return err
	}

	tlsConfig := &tls.Config{
		NextProtos:   []string{"http/1.1"},
		Certificates: make([]tls.Certificate, 1),
	}

	tlsConfig.Certificates[0], err = tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		panic(err)
	}

	tlsListener := tls.NewListener(gListener, tlsConfig)

	server := http.Server{Addr: gListener.Addr().String(), Handler: handler, TLSConfig: tlsConfig}

	go s.signalParent()

	return server.Serve(tlsListener)
}

func strSliceContains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// RunServer runs the provided server, if using TLS, TLSConfig must be setup prior to
// calling this function
func (s *Spoon) RunServer(server *http.Server) error {

	gListener, err := s.listenSetup(server.Addr)
	if err != nil {
		return err
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

	go s.signalParent()

	return server.Serve(gListener)
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

		fmt.Println("SIGUSR1 Recieved from slave")

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
					fmt.Println("Slave Shutdown! Error:", err)
				}

				fmt.Println("Slave Shutdown Complete")
			}
		}()
	}()

	if err := s.slave.Start(); err != nil {
		return fmt.Errorf("Failed to start slave process: %s", err)
	}

	return nil
}

func (s *Spoon) setupFileDescriptors(addr string) error {
	s.fileDescriptors = make([]*os.File, 1)

	a, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return fmt.Errorf("Invalid address %s (%s)", addr, err)
	}

	l, err := net.ListenTCP("tcp", a)
	if err != nil {
		return err
	}

	f, err := l.File()
	if err != nil {
		return fmt.Errorf("Failed to retreive fd for: %s (%s)", addr, err)
	}

	if err := l.Close(); err != nil {
		return fmt.Errorf("Failed to close listener for: %s (%s)", addr, err)
	}

	s.fileDescriptors[0] = f

	return nil
}
