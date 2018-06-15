package main

import "github.com/fsnotify/fsnotify"

type fileSet map[string]string

type stateT struct {
	folderHistory []fileSet
	keepGoing bool
}

func initialState() stateT {
	return stateT{
		folderHistory: []fileSet{},
	}
}

type outputT struct {
	x int
}

type inputT struct {
	x int
}

func stateToOutput(state stateT) outputT {
	return outputT{x: 0}
}

type ioResultT struct {
	x int
}

func io(eventCh chan fsnotify.Event, errCh chan error, output outputT) ioResultT {
	return ioResultT{x: 0}
}

func fileWatcher(eventCh chan fsnotify.Event, errCh chan error, folderName string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		errCh <- err
		return
	}
	for {
		select {
		case event := <-watcher.Events:
			eventCh <- event
		case err := <-watcher.Errors:
			errCh <- err
		}
	}
}

func update(state stateT, ioResult ioResultT) stateT {
	return initialState()
}

func main() {

	eventCh := make(chan fsnotify.Event, 10000)
	errCh := make(chan error, 10000)
	go fileWatcher(eventCh, errCh, "/home/true/toWatch")

	state := initialState()
	for state.keepGoing {
		state = update(state, io(eventCh, errCh, stateToOutput(state)))
	}
}
