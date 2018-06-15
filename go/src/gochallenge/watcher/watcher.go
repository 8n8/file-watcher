package main

import (
	"github.com/fsnotify/fsnotify"
	"net/http"
	"bytes"
	"io/ioutil"
	"log"
)

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
	jsonToSend []byte
	msgToPrint string
}

type inputT struct {
	x int
}

func stateToOutput(state stateT) outputT {
	return nil
}

type ioResultT struct {
	fileEvents []fsnotify.Event
	fileErrors []error
	errChOk bool
	eventChOk bool
	requestErr error
	responseBody []byte
	readBodyErr error
}

func initIoResult() ioResultT {
	return ioResultT{
		fileEvents: []fsnotify.Event,
		fileErrors: []error,
		errChOk: nil,
		eventChOk: nil,
		requestErr: nil,
		responseBody: nil,
		readBodyErr: nil,
	}
}

func io(watCh watcherChannelsT, output outputT, masterUrl string) ioResultT {

	if output.msgToPrint != nil {
		log.Print(output.msgToPrint)
	}

	result := initIoResult()

	watCh.ask <- true

	events, eventsChOk := <-watCh.events
	result.eventChOk = eventsChOk
	if !eventsChOk { return result }
	result.fileEvents = append(result.fileEvents, events)

	errors, errChOk := <- watCh.errs
	result.errChOk = errChOk
	if !errChOk { return result }
	result.fileErrors = append(result.fileErrors, errors)

	response, postErr := http.Post(
		masterUrl,
		"application/json",
		bytes.NewBuffer(output.jsonToSend))
	defer response.Body.Close()
	result.requestErr = postErr
	if postErr != nil { return result }
	body, bodyErr := ioutil.ReadAll(response.Body)
	result.responseBody = body
	result.readBodyErr = bodyErr

	return result
}

func update(state stateT, ioResult ioResultT) stateT {
	return nil
}

type watcherChannelsT struct {
	events chan []fsnotify.Event
	errs chan []error
	ask chan bool
}

func main() {
	dirToWatch := "/home/true/toWatch"
	masterUrl := "http://localhost:3000"

	watCh := watcherChannelsT{
		events: make(chan []fsnotify.Event),
		errs: make(chan []error),
		ask: make(chan bool),
	}

	go func() {
		watcher, err := fsnotify.NewWatcher()
		watcher.Add(dirToWatch)
		if err != nil {
			watCh.errs <- []error{err}
			return
		}
		var events []fsnotify.Event
		var errors []error
		for {
			select {
			case event := <-watcher.Events:
				events = append(events, event)
			case err := <-watcher.Errors:
				errors = append(errors, err)
			case <-watCh.ask:
				watCh.events <- events
				watCh.errs <- errors
			}
		}
	}()

	state := initialState()
	for state.keepGoing {
		state = update(state, io(watCh, stateToOutput(state), masterUrl))
	}
}
