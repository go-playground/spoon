package spoon

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"time"

	"github.com/inconshreveable/go-update"
)

// AutoUpdate sets up and starts the auto updating service to autoupdate you binary
// NOTE: the last param addlRequestHeaders is any other header you want added to the
// HTTP request to the Update URL, like an application key, Origin....
func (s *Spoon) AutoUpdate(updateStrategy UpdateStrategy, updateURL string, interval time.Duration, addlRequestHeaders map[string]string) (UpdatePerformed, error) {

	if s.isAutoUpdating {
		return nil, errors.New("Can only setup one AutoUpdate per application")
	}

	if s.IsSlaveProcess() {
		return nil, nil
	}

	var err error

	s.isAutoUpdating = true

	_, err = url.ParseRequestURI(updateURL)
	if err != nil {
		return nil, errors.New("Update URL Parsing Error:" + err.Error())
	}

	req, err := http.NewRequest("GET", updateURL, nil)
	if err != nil {
		return nil, errors.New("Error Creating Request:" + err.Error())
	}

	if addlRequestHeaders != nil {
		for k, v := range addlRequestHeaders {
			req.Header.Set(k, v)
		}
	}

	b, err := ioutil.ReadFile(s.binaryPath)
	if err != nil {
		return nil, err
	}

	checksum := GenerateChecksum(b)

	s.originalChecksum = checksum
	s.lastUpdateChecksum = checksum
	s.updateRequest = req
	s.updateInterval = interval
	s.updateCompleted = make(chan struct{})
	s.updateStrategy = updateStrategy

	go s.autoUpdater()

	return s.updateCompleted, nil
}

func (s *Spoon) autoUpdater() {

	for {

		select {
		case <-time.After(s.updateInterval):
			// do request

			s.logFunc("Checking for Update")

			client := &http.Client{}

			s.updateRequest.Method = "HEAD"

			resp, err := client.Do(s.updateRequest)
			if err != nil {
				s.logFunc(fmt.Sprint("ERROR Fetching Updates:", err))
				continue
			}
			defer resp.Body.Close()

			checksum := resp.Header.Get("checksum")
			if checksum == "" {
				s.logFunc("No new files for update")
				continue
			}

			if s.lastUpdateChecksum == checksum {
				continue
			}

			s.updateRequest.Method = "GET"

			resp, err = client.Do(s.updateRequest)
			if err != nil {
				s.logFunc(fmt.Sprint("ERR:", err))
			}
			defer resp.Body.Close()

			checksum = resp.Header.Get("checksum")
			if checksum == "" {
				s.logFunc("No new files for update")
				continue
			}

			// make this a helper function
			b, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				s.logFunc(fmt.Sprint("ERROR Reading Body:", err))
				continue
			}

			recvChecksum := GenerateChecksum(b)

			if checksum != recvChecksum {
				s.logFunc("checksum's do not match, checking again at the desired interval.")
				continue
			}

			s.logFunc("Checksums match! processing")

			if s.updateStrategy == FullBinary {

				if err := update.Apply(bytes.NewBuffer(b), update.Options{}); err != nil {

					if rerr := update.RollbackError(err); rerr != nil {
						s.errFunc(&BinaryUpdateError{innerError: fmt.Errorf("Failed to rollback from bad update: %v\n", rerr)})
					}
				}

			}
			// else {
			// 	if err := update.Apply(bytes.NewBuffer(b), update.Options{
			// 		Patcher: update.NewBSDiffPatcher(),
			// 	}); err != nil {
			// 		if rerr := update.RollbackError(err); rerr != nil {
			// 			fmt.Printf("Failed to rollback from bad update: %v\n", rerr)
			// 		}
			// 	}
			// }

			// update completed successfully
			s.lastUpdateChecksum = checksum
			s.updateCompleted <- struct{}{}
		}
	}
}
