// A server that watches a directory for changes to its contents,
// maintains an up-to-date list of the contents, and sends out
// messages to the master server when something changes.
package main

import (
	"bytes"
	"fmt"
	"github.com/fsnotify/fsnotify"
	"github.com/nu7hatch/gouuid"
	"gochallenge/watcher/update"
	"io/ioutil"
	"log"
	"net/http"
	"os"
)

func getFileNames(fileInfos []os.FileInfo) []string {
	result := make([]string, len(fileInfos))
	for i, inf := range fileInfos {
		result[i] = inf.Name()
	}
	return result
}

func listToMap(list []string) map[string]bool {
	m := map[string]bool{}
	for _, l := range list {
		m[l] = true
	}
	return m
}

// It does all the IO for the main loop.  This could be:
// 1. printing a message
// 2. sending a message to the master server
// 3. receiving a message from the file watching goroutine
func io(watCh watcherChannelsT, output update.OutputT, masterUrl string) update.IoResultT {

	if output.MsgToPrint != "" {
		log.Print(output.MsgToPrint)
	}

	result := update.InitIoResult()

	if len(output.JsonToSend) > 0 {
		response, postErr := http.Post(
			masterUrl,
			"application/json",
			bytes.NewBuffer(output.JsonToSend))
		result.RequestErr = postErr
		if postErr != nil {
			return result
		}
		defer response.Body.Close()
		body, bodyErr := ioutil.ReadAll(response.Body)
		result.ResponseBody = body
		result.ReadBodyErr = bodyErr
		return result
	}

	result.NewGuid = newGuid()

	event, eventsChOk := <-watCh.events
	result.EventChOk = eventsChOk
	result.FileEvent = event

	select {
	case fileError, errChOk := <-watCh.errs:
		result.ErrChOk = errChOk
		result.FileError = fileError
	default:
	}
	result.NewEvents = true

	return result
}

func newGuid() string {
	guidBytes, _ := uuid.NewV4()
	return guidBytes.String()
}

type watcherChannelsT struct {
	events chan fsnotify.Event
	errs   chan error
	ask    chan bool
}

// This function gets run as a separate goroutine.  It continuously
// watches the given directory and sends any changes down the given channels.
func fileWatcher(watCh watcherChannelsT, dirToWatch string) {
	watcher, err := fsnotify.NewWatcher()
	watcher.Add(dirToWatch)
	if err != nil {
		watCh.errs <- err
		return
	}
	for {
		select {
		case event := <-watcher.Events:
			if event.Op != fsnotify.Write && event.Op != fsnotify.Chmod {
				watCh.events <- event
			}
		case err := <-watcher.Errors:
			watCh.errs <- err
		}
	}
}

const maxChanBuf = 10000

func main() {
	dirToWatch := os.Args[1]
	masterUrl := "http://localhost:3000"
	fileList, err := ioutil.ReadDir(dirToWatch)

	if err != nil {
		fmt.Println(err)
		return
	}

	watCh := watcherChannelsT{
		events: make(chan fsnotify.Event, maxChanBuf),
		errs:   make(chan error, maxChanBuf),
		ask:    make(chan bool),
	}

	go fileWatcher(watCh, dirToWatch)

	state := update.InitState(listToMap(getFileNames(fileList)), newGuid())
	for state.KeepGoing {
		state = update.Update(state, io(watCh, update.StateToOutput(state, dirToWatch), masterUrl))
	}
}
