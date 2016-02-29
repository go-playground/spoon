package main

import "github.com/go-playground/spoon"

func main() {

	spoon.ServerAutoUpdate(spoon.FullBinary, "/app/updates", ":3006", "files/client")

	close := make(chan bool)

	<-close // wait forever
}
