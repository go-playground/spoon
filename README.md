Package spoon
=============
![Project status](https://img.shields.io/badge/version-0.8.0-yellowgreen.svg)

Package spoon is used to create zero-downtime upgradable programs and includes logic to gracefully handle TCP connections, including HTTP.

NOTE: I've been using this in a production app for a few months without any problems whatsoever, so it should be good and stable.

Contrived Example - using HTTP
-----------------

```go

package main

import (
	"log"
	"net/http"

	"github.com/go-playground/lars"
	"github.com/go-playground/lars/examples/middleware/logging-recovery"
	"github.com/go-playground/spoon"
)

func main() {

	// setup TCP connections
	sp := spoon.New()

	ltcp, err := sp.ListenTCP(":4444")
	if err != nil {
		panic(err)
	}

	// let spoon know your; done setting up connections... if any
	err = sp.ListenerSetupComplete()
	if err != nil {
		panic(err)
	}

	l := lars.New()
	l.Use(middleware.LoggingAndRecovery)

	l.Get("/", GetHome)

	// OPTIONAL: use TCP connection for HTTP, could have just used raw connection for non HTTP logic.
	err = ltcp.ListenAndServe(":4444", l.Serve())
	if err != nil {
		panic(err)
	}

	// Run, will block here
	sp.Run()
}

// GetHome ...
func GetHome(l lars.Context) {

	if err := l.Text(http.StatusOK, "Why spoon? because it doesn't fork!"); err != nil {
		log.Panic(err)
	}
}


```