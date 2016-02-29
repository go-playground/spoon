#!/bin/bash

cd client
sed -i.bak -e 's/Old /New /g' main.go
go build
mv client ../server/files
sed -i.bak -e 's/New /Old /g' main.go
go build
rm main.go.bak
