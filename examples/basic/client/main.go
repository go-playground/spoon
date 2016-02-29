package main

import (
	"log"
	"net/http"
	"time"

	"github.com/go-playground/spoon"
)

func main() {

	var handler http.Handler

	sp := spoon.New()

	// when the program first initiates the master process sp.IsSlaveProcess() == false and no need to incur the overhead of
	// setting up hanlders, logs, DB connections... nor the memory they consume.
	if !sp.IsSlaveProcess() {

		// updated must be consumed, even if not doing automatic restarts
		updated, err := sp.AutoUpdate(spoon.FullBinary, "http://localhost:3006/app/updates", time.Second*15, nil)
		if err != nil {
			panic(err)
		}

		sp.SetGracefulRestartChannel(updated)
		sp.SetForceTerminationTimeout(time.Second * 5)
	} else {
		handler = setup()
	}

	sp.ListenAndServe(":3007", handler)
}

func setup() http.Handler {
	// setup logging, DB connections, routes............

	http.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		log.Println("Old Binary")
		w.Write([]byte("Old Binary"))
	})

	return http.DefaultServeMux
}
