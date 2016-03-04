package spoon

import "fmt"

// SlaveStartError contains the slave start error
type SlaveStartError struct {
	innerError error
}

// Error returns the slave start error
func (s *SlaveStartError) Error() string {
	return fmt.Sprint("Slave Process Failed to Start:" + s.innerError.Error())
}

var _ error = new(SlaveStartError)

// SlaveShutdownError contains the slave shutdown error
type SlaveShutdownError struct {
	innerError error
}

// Error returns the slave start error
func (s *SlaveShutdownError) Error() string {
	return fmt.Sprint("Slave Shutdown Error:" + s.innerError.Error() + "\n\nNOTE: could just be the force termination because timeout reached")
}

var _ error = new(SlaveShutdownError)

// FileDescriptorError contains a file descriptor error
type FileDescriptorError struct {
	innerError error
}

// Error returns the slave start error
func (f *FileDescriptorError) Error() string {
	return fmt.Sprint("FileDescriptor Error:" + f.innerError.Error())
}

var _ error = new(FileDescriptorError)

// SignalParentError contains an error regarding signaling the parent
type SignalParentError struct {
	innerError error
}

// Error returns the slave start error
func (s *SignalParentError) Error() string {
	return fmt.Sprint("Error Signaling parent:" + s.innerError.Error())
}

var _ error = new(SignalParentError)

// BinaryUpdateError contains a binary update error
type BinaryUpdateError struct {
	innerError error
}

// Error returns the slave start error
func (b *BinaryUpdateError) Error() string {
	return fmt.Sprint("Binary Update Error:" + b.innerError.Error())
}

var _ error = new(BinaryUpdateError)
