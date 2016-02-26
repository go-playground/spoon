package spoon

import (
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

// IsSlaveProcess returns if the current process is the main master process or slave,
//
// helps is determining when to load up all your handler dependencies i.e.
// DB connections, caches...
func (s *Spoon) IsSlaveProcess() bool {
	return os.Getenv(envListenerFDS) != ""
}

// Run sets up the addr listener File Descriptors and runs the application
func (s *Spoon) Run(addr string) error {
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

// ListenAndServe mimics the std libraries http.ListenAndServe
func (s *Spoon) ListenAndServe(addr string, handler http.Handler) error {

	fds := os.Getenv(envListenerFDS)

	// first server, let's call it master, to get started
	if fds == "" {
		return s.Run(addr)
	}

	forceTerm := os.Getenv(envForceTerminationNS)
	keepAlive := os.Getenv(envKeepAliveNS)

	if i, err := strconv.ParseInt(forceTerm, 10, 64); err == nil {
		s.SetForceTerminationTimeout(time.Duration(i))
	}

	if i, err := strconv.ParseInt(keepAlive, 10, 64); err == nil {
		s.SetKeepAliveDuration(time.Duration(i))
	}

	// is a slave process
	// do slave logc to get File Descriptors

	// numFDs, err := strconv.Atoi(os.Getenv(envListenerFDS))
	// if err != nil {
	// 	return fmt.Errorf("invalid %s integer", envListenerFDS)
	// }

	// for i := 0; i < numFDs; i++ {

	f := os.NewFile(uintptr(3), "")

	l, err := net.FileListener(f)
	if err != nil {
		return fmt.Errorf("failed to inherit file descriptor: %d", 0)
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

	http.Serve(gListener, handler)

	return nil
}

func (s *Spoon) startSlave() error {

	e := os.Environ()
	e = append(e, envListenerFDS+"="+strconv.Itoa(len(s.fileDescriptors)))
	e = append(e, envForceTerminationNS+"="+strconv.FormatInt(s.forceTerminateTimeout.Nanoseconds(), 10))
	e = append(e, envKeepAliveNS+"="+strconv.FormatInt(s.keepaliveDuration.Nanoseconds(), 10))

	// start server
	oldCmd := s.slave

	s.slave = exec.Command(s.binaryPath)
	s.slave.Env = e
	s.slave.Args = os.Args
	s.slave.Stdin = os.Stdin
	s.slave.Stdout = os.Stdout
	s.slave.Stderr = os.Stderr
	s.slave.ExtraFiles = s.fileDescriptors

	if err := s.slave.Start(); err != nil {
		return fmt.Errorf("Failed to start slave process: %s", err)
	}

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
