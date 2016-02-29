#!/bin/bash

cd client
sed -i -e 's/Old/New/g' main.go
go build
mv client ../server/files
sed -i -e 's/New/Old/g' main.go
go build
