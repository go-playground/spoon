package spoon

import (
	"io/ioutil"
	"net/http"
	"os"
)

type updateHandler struct {
	filename       string
	updateStrategy UpdateStrategy
}

// ServerAutoUpdate calls ServerAutoUpdateHandler to get the handler
// and runs an http server in another goroutine
func ServerAutoUpdate(updateStrategy UpdateStrategy, url string, addr string, filename string) {
	handler := ServerAutoUpdateHandler(updateStrategy, filename)

	http.Handle(url, handler)
	go http.ListenAndServe(addr, nil)
}

// ServerTLSAutoUpdate calls ServerAutoUpdateHandler to get the handler
// and runs an http TLS server in another goroutine
func ServerTLSAutoUpdate(updateStrategy UpdateStrategy, url string, addr string, filename string, certFile string, keyFile string) {
	handler := ServerAutoUpdateHandler(updateStrategy, filename)

	http.Handle(url, handler)
	go http.ListenAndServeTLS(addr, certFile, keyFile, nil)
}

// ServerAutoUpdateHandler returns the servers http.HandlerFunc
// for registration in your ServerMux if you wish to avoid setting up your own
// you may call ServerAutoUpdate to start a server within another goroutine.
func ServerAutoUpdateHandler(updateStrategy UpdateStrategy, filename string) http.Handler {

	return &updateHandler{
		filename:       filename,
		updateStrategy: updateStrategy,
	}
}

func (hf *updateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	if r.Method != "GET" && r.Method != "HEAD" {
		return
	}

	_, err := os.Stat(hf.filename)
	if err != nil {
		// no file exists on disk
		return
	}

	b, err := ioutil.ReadFile(hf.filename)
	if err != nil {
		http.Error(w, "Error Reading File", http.StatusInternalServerError)
		return
	}

	checksum := GenerateChecksum(b)

	// fmt.Println(checksum)
	w.Header().Set("checksum", checksum)

	if r.Method == "GET" {
		w.Write(b)
	}
}
