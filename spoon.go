package spoon

import (
	"log"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/kardianos/osext"
)

// LogFunc is the function that all log messages get sent to,
// it is overridable using the RegisterLogFunc on the spoon instance.
type LogFunc func(msg string)

// ErrorFunc is the function that all error messages get sent to,
// is is overridable using the RegisterErrorFunc
type ErrorFunc func(err error)

// UpdateStrategy specifies the Update Strategy
// NOTE: All strategies use Checksum + CryptoGraphic signature
type UpdateStrategy uint

// UpdatePerformed is the channel type used to signify an update has been completed
type UpdatePerformed chan struct{}

// Update Strategies
const (
	FullBinary UpdateStrategy = iota
	// Patch
)

// Spoon is the instance object
type Spoon struct {
	updateStrategy           UpdateStrategy
	updateInterval           time.Duration
	originalChecksum         string
	lastUpdateChecksum       string
	updateRequest            *http.Request
	updateCompleted          UpdatePerformed
	isAutoUpdating           bool
	binaryPath               string
	fileDescriptors          []*os.File
	slave                    *exec.Cmd
	forceTerminateTimeout    time.Duration // default is 5 minutes
	keepaliveDuration        time.Duration
	gracefulShutdownComplete chan struct{}
	gracefulRestartChannel   chan struct{}
	logFunc                  LogFunc
	errFunc                  ErrorFunc
}

// New creates a new spoon instance
func New() *Spoon {

	executable, err := osext.Executable()
	if err != nil {
		panic(err)
	}

	return &Spoon{
		binaryPath:            executable,
		forceTerminateTimeout: time.Minute * 5,
		keepaliveDuration:     time.Minute * 3,
		logFunc: func(msg string) {
			log.Println(msg)
		},
		errFunc: func(err error) {
			log.Println(err)
		},
	}
}

// RegisterErrorFunc registers a custom error function which all errors
// will be sent to and the calling program can handle any way they wish
func (s *Spoon) RegisterErrorFunc(fn ErrorFunc) {
	s.errFunc = fn
}

// RegisterLogFunc registers a custom log function which all logs
// will be sent to and the calling program can handle any way they wish
func (s *Spoon) RegisterLogFunc(fn LogFunc) {
	s.logFunc = fn
}

// SetForceTerminationTimeout sets the duartion to wait before force
// terminating remaining connections
// DEFAULT: 5 minutes
func (s *Spoon) SetForceTerminationTimeout(d time.Duration) {
	s.forceTerminateTimeout = d
}

// SetKeepAliveDuration sets the connections Keep Alive Period
// DEFAULT: 3 minutes
func (s *Spoon) SetKeepAliveDuration(d time.Duration) {
	s.keepaliveDuration = d
}

// SetGracefulRestartChannel sets the channel that will trigger
// a gracefull restart. Exists this way so that you can restart whenever you want,
// not just when an upgrade is performed. i.e. maybe you want to rollout your
// update to all servers but not restart until the start of the hour, ensuring
// all your frontends are already updated and waiting.
func (s *Spoon) SetGracefulRestartChannel(ch chan struct{}) {
	s.gracefulRestartChannel = ch
}

// HasBeenUpdated returns if the running binary has been updated since it's been started.
// good for applications that may need manual intervention to restart, this will allow
// the application to know it needs restarting and notify user ( think how google chrome does it )
func (s *Spoon) HasBeenUpdated() bool {
	return s.originalChecksum != s.lastUpdateChecksum
}
