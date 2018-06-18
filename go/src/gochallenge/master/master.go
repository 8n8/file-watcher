package main

import (
	"goji.io"
	"goji.io/pat"
	"net/http"
	"io/ioutil"
	"fmt"
	"encoding/json"
)

type folderState struct {
	fileSet map[string]bool
	guidOfLastUpdate string
}

type stateT struct {
	folderFileSets map[string]folderState
	masterList []string
	keepGoing bool
}

type ioResultT struct {
	rawWatcherInput rawWatcherInputT
	clientRequest chan []byte
}

type responseToClientT struct {
	none bool
	content []byte
	ch chan []byte
}

type outputT struct {
	responseToClient responseToClientT
	msgToPrint string
}

func stateToOutput(state stateT) outputT {
	return outputT{}
}

func io(output outputT, chs serverChannelsT) ioResultT {
	if output.msgToPrint != "" {
		fmt.Println(output.msgToPrint)
	}
	if !output.responseToClient.none {
		output.responseToClient.ch <- output.responseToClient.content
	}
	return ioResultT{
		rawWatcherInput: <-chs.watcherInput,
		clientRequest: <-chs.clientRequest,
	}
}

func updateFolderFileSets(rawWatcherInput rawWatcherInputT, folderFileSets map[string]folderState) (map[string]folderState, error) {
	var watcherInput watcherInputT
	jsonErr := json.Unmarshal(rawWatcherInput.content, &watcherInput)
	if jsonErr != nil {
		return jsonErr
	}
}

func update(state stateT, ioResult ioResultT) stateT {
	err, newFolderFileSets := updateFolderFileSets(ioResult.rawWatcherInput, state.folderFileSets)
	return stateT{
		folderFileSets: newFolderFileSets,
	}
}

type watcherInputT struct {
	guid string
	deletions []string
	creations []string
	completeList []string
	dirWatching string
}

type rawWatcherInputT struct {
	content []byte
	replyChan chan []byte
	bodyReadErr error
}

type serverChannelsT struct {
	clientRequest chan (chan []byte)
	watcherInput chan rawWatcherInputT
	err chan error
}

const maxBufferSize = 1000

func main() {

	serCh := serverChannelsT{
		clientRequest: make(chan (chan []byte), maxBufferSize),
		watcherInput: make(chan rawWatcherInputT, maxBufferSize),
		err: make(chan error),
	}

	go func() {
		clientRequest := func(w http.ResponseWriter, r *http.Request) {
			if len(serCh.clientRequest) == maxBufferSize {
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte("Server too busy."))
				return
			}
			replyChan := make(chan []byte)
			serCh.clientRequest <- replyChan
			w.Write(<-replyChan)
		}

		watcherInput := func(w http.ResponseWriter, r *http.Request) {
			if len(serCh.watcherInput) == maxBufferSize {
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte("Server too busy."))
				return
			}
			body, err := ioutil.ReadAll(r.Body)
			replyChan := make(chan []byte)
			serCh.watcherInput <- rawWatcherInputT{
				content: body,
				replyChan: replyChan,
				bodyReadErr: err,
			}
		}

		mux := goji.NewMux()
		mux.HandleFunc(pat.Get("/"), clientRequest)
		mux.HandleFunc(pat.Post("/"), watcherInput)
		http.ListenAndServe("localhost:3000", mux)
	}()

	state := stateT{}
	for state.keepGoing {
		state = update(state, io(stateToOutput(state), serCh))
	}
}