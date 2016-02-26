package spoon

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
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
		panic("Can only setup one AutoUpdate per application")
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

	s.updateRequest = req
	s.updateInterval = interval
	s.updateCompleted = make(chan struct{})
	s.updateStrategy = updateStrategy

	go s.autoUpdater()

	return s.updateCompleted, nil
}

func (s *Spoon) autoUpdater() {

	// go func(completed UpdatePerformed) {
	// 	for {
	// 		select {
	// 		case <-completed:
	// 			fmt.Println("Update completed")
	// 		}
	// 	}
	// }(s.updateCompleted)

	for {

		select {
		case <-time.After(s.updateInterval):
			// do request

			fmt.Println("Checking for Update")

			client := &http.Client{}

			s.updateRequest.Method = "HEAD"

			resp, err := client.Do(s.updateRequest)
			if err != nil {
				log.Println("ERR:", err)
			}
			defer resp.Body.Close()

			checksum := resp.Header.Get("checksum")
			if checksum == "" {
				fmt.Println("No new files for update")
				continue
			}

			if s.lastUpdateChecksum == checksum {
				continue
			}

			s.updateRequest.Method = "GET"

			resp, err = client.Do(s.updateRequest)
			if err != nil {
				log.Println("ERR:", err)
			}
			defer resp.Body.Close()

			checksum = resp.Header.Get("checksum")
			if checksum == "" {
				fmt.Println("No new files for update")
				continue
			}

			// make this a helper function
			b, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				log.Println("ERROR Reading Body:", err)
			}

			recvChecksum := GenerateChecksum(b)

			if checksum != recvChecksum {
				fmt.Println("checksum's do not match, checking again at the desired interval.")
				continue
			}

			fmt.Println("Checksums match! processing")

			if s.updateStrategy == FullBinary {

				if err := update.Apply(bytes.NewBuffer(b), update.Options{}); err != nil {

					if rerr := update.RollbackError(err); rerr != nil {
						fmt.Printf("Failed to rollback from bad update: %v\n", rerr)
					}
				}

			} else {
				if err := update.Apply(bytes.NewBuffer(b), update.Options{
					Patcher: update.NewBSDiffPatcher(),
				}); err != nil {
					if rerr := update.RollbackError(err); rerr != nil {
						fmt.Printf("Failed to rollback from bad update: %v\n", rerr)
					}
				}
			}

			// update completed successfully
			s.lastUpdateChecksum = checksum
			s.updateCompleted <- struct{}{}
		}
	}
}
