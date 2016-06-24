package spoon

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/kardianos/osext"
)

const (
	envListenerFDS = "GO_LISTENER_FDS"
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
	}
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

	fds := envListenerFDS + "=" + strconv.Itoa(len(s.fileDescriptors))
	e := append(os.Environ(), fds)

	// start server
	oldCmd := s.child

	s.child = exec.Command(s.binaryPath)
	s.child.Env = e
	s.child.Args = os.Args
	s.child.Stdin = os.Stdin
	s.child.Stdout = os.Stdout
	s.child.Stderr = os.Stderr
	s.child.ExtraFiles = s.fileDescriptors

	// wait for close signals here
	signals := make(chan os.Signal)
	signal.Notify(signals, syscall.SIGTERM)

	go func() {

		// wait for child to signal it is up and running
		<-signals // child notifies master process that it's up and running

		s.logFunc("SIGUSR1 Recieved from child")

		signal.Stop(signals)

		// will have to use current s.child, if set, to trigger shutdown of old child
		if oldCmd != nil {
			// shut down server! .. gracefully
			oldCmd.Process.Signal(syscall.SIGTERM)
		}

		cmdwait := make(chan error)
		go func() {
			// wait for child process to finish, one way or another
			cmdwait <- s.child.Wait()
		}()

		go func() {
			select {
			case err := <-cmdwait:
				if err != nil {
					s.errFunc(&ChildShutdownError{innerError: err})
				}

				s.logFunc("Child Shutdown Complete")
			}
		}()
	}()

	if err := s.child.Start(); err != nil {
		return &ChildStartError{innerError: err}
	}

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
}

// ListenTCP announces on the local network address laddr. The network net must
// be: "tcp", "tcp4" or "tcp6". It returns an inherited net.Listener for the
// matching network and address, or creates a new one using net.ListenTCP.
func (s *Spoon) ListenTCP(nett string, addr string) (Listener, error) {

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
