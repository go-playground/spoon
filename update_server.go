package spoon

import (
	"io/ioutil"
	"net/http"
	"os"
)

type updateHandler struct {
	filename       string
	updateStrategy UpdateStrategy
	url            string
}

type wrapHandler struct {
	preHandler PreHandler
	next       http.Handler
}

func (wh *wrapHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	wh.preHandler(wh.next, w, r)
}

// PreHandler used if some sort of pre auth check or something needs to be done prior
// for checking for an update.
type PreHandler func(next http.Handler, w http.ResponseWriter, r *http.Request)

// ServerAutoUpdate calls ServerAutoUpdateHandler to get the handler
// and runs an http server in another goroutine
func ServerAutoUpdate(updateStrategy UpdateStrategy, url string, addr string, filename string, preHandler PreHandler) {

	handler := ServerAutoUpdateHandler(updateStrategy, filename, url)

	if preHandler != nil {
		next := handler
		handler = &wrapHandler{
			preHandler: preHandler,
			next:       next,
		}
	}

	go http.ListenAndServe(addr, handler)
}

// ServerTLSAutoUpdate calls ServerAutoUpdateHandler to get the handler
// and runs an http TLS server in another goroutine
func ServerTLSAutoUpdate(updateStrategy UpdateStrategy, url string, addr string, filename string, certFile string, keyFile string, preHandler PreHandler) {

	handler := ServerAutoUpdateHandler(updateStrategy, filename, url)

	if preHandler != nil {
		next := handler
		handler = &wrapHandler{
			preHandler: preHandler,
			next:       next,
		}
	}

	go http.ListenAndServeTLS(addr, certFile, keyFile, handler)
}

// ServerAutoUpdateHandler returns the servers http.HandlerFunc
// for registration in your ServerMux if you wish to avoid setting up your own
// you may call ServerAutoUpdate to start a server within another goroutine.
func ServerAutoUpdateHandler(updateStrategy UpdateStrategy, filename string, url string) http.Handler {

	return &updateHandler{
		filename:       filename,
		updateStrategy: updateStrategy,
		url:            url,
	}
}

func (hf *updateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	if (r.Method != "GET" && r.Method != "HEAD") || r.URL.Path != hf.url {
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
