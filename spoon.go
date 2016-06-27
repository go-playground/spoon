package spoon

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/kardianos/osext"
)

const (
	envListenerFDS = "GO_LISTENER_FDS"
	envActive      = "GO_ACTIVE_PROCESSES"
)

// Spoon contains one or more connection information
// for graceful restarts
type Spoon struct {
	binaryPath          string
	fileDescriptors     []*os.File
	fileDescriptorIndex int
	listeners           []net.Listener
	activeProcesses     *int32
	child               *exec.Cmd
	errorChan           chan error
	restartChan         chan struct{}
}

// New creates a new spoon instance.
func New() *Spoon {

	executable, err := osext.Executable()
	if err != nil {
		panic(err)
	}

	var active int32

	return &Spoon{
		binaryPath:          executable,
		fileDescriptorIndex: 3, // they start at 3
		activeProcesses:     &active,
		errorChan:           make(chan error),
		restartChan:         make(chan struct{}),
	}
}

func (s *Spoon) startChild() error {

	fds := envListenerFDS + "=" + strconv.Itoa(len(s.fileDescriptors))
	ap := envActive + "=" + strconv.Itoa(int(atomic.LoadInt32(s.activeProcesses)))
	e := append(os.Environ(), fds, ap)

	oldCmd := s.child

	// start server
	s.child = exec.Command(s.binaryPath)
	s.child.Env = e
	s.child.Args = os.Args
	s.child.Stdin = os.Stdin
	s.child.Stdout = os.Stdout
	s.child.Stderr = os.Stderr
	s.child.ExtraFiles = s.fileDescriptors

	// wait for close signals here
	signals := make(chan os.Signal)
	signal.Notify(signals, syscall.SIGUSR1)

	go func() {

		// wait for child to signal it is up and running
		<-signals // child notifies master process that it's up and running

		log.Println("SIGUSR1 Recieved from child")

		signal.Stop(signals)

		// keep track of active processes to ensure at least one is running
		atomic.AddInt32(s.activeProcesses, 1)

		// will have to use current s.child, if set, to trigger shutdown of old child
		if oldCmd != nil {
			// shut down old server! .. gracefully
			oldCmd.Process.Signal(syscall.SIGTERM)
		}

		go func() {

			err := s.child.Wait()
			if err != nil {
				s.sendError(&ChildShutdownError{innerError: err})
			}

			log.Println("Child Shutdown Complete")

			atomic.AddInt32(s.activeProcesses, -1)

			// ensure that at least one instance is running
			// this is just in case the child process crashes
			// due to an unexpected error and the maaster process
			// is still running, but without any children!
			//
			// a little outside the scope of this library but better to be up
			// than not!

			if atomic.LoadInt32(s.activeProcesses) == 0 {

				// no children running!... start one back up!
				// and notify of error.

				s.sendError(&ChildCrashError{innerError: errors.New("Unexpected Child End of Process, attempting restart")})

				err := s.startChild()
				if err != nil {
					s.sendError(&ChildStartError{innerError: err})
				}

			}
		}()
	}()

	if err := s.child.Start(); err != nil {
		return &ChildStartError{innerError: err}
	}

	return nil
}

// Restart triggers a service zero downtime restart
func (s *Spoon) Restart() {

	// if in slave signal master process
	// with syscall.SIGUSR2 which will cause
	// restart
	if s.isSlaveProcess() {
		go s.signalParent(syscall.SIGUSR2)
		return
	}

	s.restartChan <- struct{}{}
}

// ListenerSetupComplete when in the master process, starts the first child
// otherwise if in the child process is just ignored.
func (s *Spoon) ListenerSetupComplete() error {

	// not need to do anything
	if s.isSlaveProcess() {
		return nil
	}

	// in master process, start new child process
	// blocks until getting a termination signal.

	// starting goroutine to monitor restart signals from children
	go func() {

		signals := make(chan os.Signal)
		signal.Notify(signals, syscall.SIGUSR2)

		for {
			<-signals

			log.Println("Recieved Restart signal from Child")

			s.Restart()
		}
	}()

	if err := s.startChild(); err != nil {
		return err
	}

	go func() {
		for {
			<-s.restartChan

			log.Println("Graceful restart triggered")

			// graceful restart triggered
			err := s.startChild()
			if err != nil {
				s.sendError(&ChildStartError{innerError: fmt.Errorf("ERROR starting new slave gracefully %s", err)})
			}
		}
	}()

	// wait for close signals here
	signals := make(chan os.Signal)
	signal.Notify(signals, syscall.SIGTERM)

	<-signals

	return nil
}

// Run when in the child process, notifies the master process that it has completed startup.
// and will block until program is shutdown.
func (s *Spoon) Run() {

	// should never reach this code from master process as
	// ListenerSetupComplete() should block.
	// but just in case
	if !s.isSlaveProcess() {
		panic("ERROR: Run should never be called from master process, please check that ListenerSetupComplete() was called.")
	}

	done := make(chan bool)
	signals := make(chan os.Signal)
	signal.Notify(signals, syscall.SIGTERM)

	go func() {
		<-signals

		log.Println("TERMINATION SIGNAL RECEIVED, Closing Slave Listener(s)")

		closed := make(chan int)
		var mt sync.Mutex
		var i int

		for _, l := range s.listeners {
			go func(l net.Listener) {
				err := l.Close()

				mt.Lock()
				i++

				log.Printf("Gracefully shutdown server %d of %d\n", i, len(s.listeners))
				if err != nil {
					log.Println("There was an error shutting down the listener:", err, " continuing shutdown")
				}

				if i == len(s.listeners) {
					closed <- 0
				}

				mt.Unlock()
			}(l)
		}

		os.Exit(<-closed)
	}()

	// let's just wait a few seconds to ensure all listeners have completed startup
	// I don't know of a way to tell if they are already running or not 100%, the stdlib
	// has no way to hook into it that I know of.
	//
	// if 0 then it's first slave to be started, don't wait!
	if s.getExtraParams(envActive) != "0" {
		time.Sleep(time.Second * 3)
	}

	go s.signalParent(syscall.SIGUSR1)

	<-done
}

// Errors returns an error channel that can optionally be listened to
// if you need to know if an error, or a specific error, has occurred.
// eg. I send the dev team an email when something went wrong ( which should be never )
// but just in case.
func (s *Spoon) Errors() <-chan error {

	if s.errorChan == nil {
		s.errorChan = make(chan error)
	}

	return s.errorChan
}

func (s *Spoon) sendError(err error) {

	if s.errorChan == nil {
		log.Println(err)
	} else {
		s.errorChan <- err
	}
}

func (s *Spoon) signalParent(sig os.Signal) {

	time.Sleep(time.Second)
	pid := os.Getppid()

	proc, err := os.FindProcess(pid)
	if err != nil {
		s.sendError(&SignalParentError{fmt.Errorf("ERROR FINDING MASTER PROCESS: %s", err)})
	}

	err = proc.Signal(sig)
	if err != nil {
		s.sendError(&SignalParentError{fmt.Errorf("ERROR SIGNALING MASTER PROC: %s", err)})
	}
}

// ListenTCP announces on the local network address laddr. The network net must
// be: "tcp", "tcp4" or "tcp6". It returns an inherited net.Listener for the
// matching network and address, or creates a new one using net.ListenTCP.
func (s *Spoon) ListenTCP(addr string) (Listener, error) {

	if !s.isSlaveProcess() {

		// setup file descriptors

		a, err := net.ResolveTCPAddr("tcp", addr)
		if err != nil {
			return nil, &FileDescriptorError{innerError: fmt.Errorf("Invalid address %s (%s)", addr, err)}
		}

		l, err := net.ListenTCP("tcp", a)
		if err != nil {
			return nil, &FileDescriptorError{innerError: err}
		}

		f, err := l.File()
		if err != nil {
			return nil, &FileDescriptorError{innerError: fmt.Errorf("Failed to retreive fd for: %s (%s)", addr, err)}
		}

		if err := l.Close(); err != nil {
			return nil, &FileDescriptorError{innerError: fmt.Errorf("Failed to close listener for: %s (%s)", addr, err)}
		}

		s.fileDescriptors = append(s.fileDescriptors, f)

		return nil, nil
	}

	f := os.NewFile(uintptr(s.fileDescriptorIndex), "")
	s.fileDescriptorIndex++

	l, err := net.FileListener(f)
	if err != nil {
		fmt.Println(err)
		return nil, &FileDescriptorError{innerError: fmt.Errorf("failed to inherit file descriptor: %d error: %s", s.fileDescriptorIndex, err)}
	}

	ltcp := newtcpListener(l.(*net.TCPListener))
	s.listeners = append(s.listeners, ltcp)

	return ltcp, nil
}

func (s *Spoon) isSlaveProcess() bool {
	return s.getExtraParams(envListenerFDS) != ""
}

func (s *Spoon) getExtraParams(key string) string {
	return os.Getenv(key)
}
