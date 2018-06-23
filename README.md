# Solution to code challenge: file monitoring with microservices in Go

The task is to maintain an up-to-date flat list of the files in many directories, using a separate server for each directory, and a master server to collate the results and respond to client requests.

## Description

The master server listens for input from the watcher servers.  Each watcher maintains a list of the files in a directory, and communicates any changes to the master.  There should be one instance of the master and as many as required of the watcher.  The master responds to client GET requests with a single list of all the files in all the directories watched by the watchers.

## Install

Tested on Ubuntu 16.04, with Go 1.10.1.

Set the GOPATH to /path/to/this/repository/go.  Build with ```go build watcher``` in go/src/gochallenge/watcher and ```go build master``` in go/src/gochallenge/master.  The dependencies (installable with ```go get```) are

+ github.com/fsnotify/fsnotify
+ github.com/nu7hatch/gouuid
+ goji.io/pat

## Run

Run the master server without any arguments.  To get the list of files as JSON, do a GET request to http://localost:3000/files .  Run the watcher with one argument, which should be the absolute path of the directory to watch.

## Todo

If I spent more time on this project, I would:

1. Write a script to test with several thousand watchers and many files, to see how it copes with a large load.
2. Write tests for the update functions in master/update and watcher/update.  These are pure functions and should be easy to test.
3. Think about reducing the amount of passing by value of large structs.  There is a lot of this in both servers.  It might be that it would be sensible to reduce the amount of copying by using pointers in the inputs of some of the update functions.  This would depend on the results of the load tests.
