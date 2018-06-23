// The master server.  It:
// 1) processes POSTs from the watcher servers
// 2) maintains the master file list
// 3) responds to client requests for the master file list
package main

import (
	"fmt"
	"gochallenge/master/update"
	"goji.io"
	"goji.io/pat"
	"io/ioutil"
	"net/http"
)

// It does the input and output for the main loop: printing messages,
// responding to client requests and communicating with the watcher servers.
func io(output update.OutputT, chs serverChannelsT) update.IoResultT {
	result := update.IoResultT{}
	if output.MsgToPrint != "" {
		fmt.Println(output.MsgToPrint)
	}
	if len(output.ResponseToClient.Content) != 0 {
		output.ResponseToClient.Ch <- output.ResponseToClient.Content
	}
	if len(output.ResponseToWatcher.Msg) != 0 {
		output.ResponseToWatcher.Ch <- output.ResponseToWatcher.Msg
	}
	select {
	case clientRequest := <-chs.clientRequest:
		result.ClientRequest = clientRequest
		result.ClientOrWatcher = update.ClientRequestC
	case rawWatcherInput := <-chs.watcherInput:
		result.RawWatcherInput = rawWatcherInput
		result.ClientOrWatcher = update.WatcherInputC
		return result
	}
	return result
}

type serverChannelsT struct {
	clientRequest chan (chan []byte)
	watcherInput  chan update.RawWatcherInputT
	err           chan error
}

const maxBufferSize = 1000

type handlerFunc func(http.ResponseWriter, *http.Request)

// Makes the function for responding to watcher inputs.  The inputs
// are put into a buffered channel that communicates with the main loop.
func makeWatcherInputHandler(serCh serverChannelsT) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if len(serCh.watcherInput) == maxBufferSize {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("Server too busy."))
			return
		}
		body, err := ioutil.ReadAll(r.Body)
		replyChan := make(chan []byte)
		serCh.watcherInput <- update.RawWatcherInputT{
			Content:     body,
			ReplyChan:   replyChan,
			BodyReadErr: err,
		}
		resp := <-replyChan
		w.Write(resp)
	}
}

// Makes the function for dealing with client requests.  The requests are
// put into a buffered channel to await attention by the main loop.
func makeClientRequestHandler(serCh serverChannelsT) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if len(serCh.clientRequest) == maxBufferSize {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("Server too busy."))
			return
		}
		replyChan := make(chan []byte)
		serCh.clientRequest <- replyChan
		w.Write(<-replyChan)
	}
}

// Sets up the routes for the server.  It is run inside a separate go routine
// and communicates with the main loop via channels.
func server(serCh serverChannelsT) {
	mux := goji.NewMux()
	mux.HandleFunc(pat.Get("/files"), makeClientRequestHandler(serCh))
	mux.HandleFunc(pat.Post("/"), makeWatcherInputHandler(serCh))
	http.ListenAndServe("localhost:3000", mux)
}

func main() {
	serCh := serverChannelsT{
		clientRequest: make(chan (chan []byte), maxBufferSize),
		watcherInput:  make(chan update.RawWatcherInputT, maxBufferSize),
		err:           make(chan error),
	}

	go server(serCh)

	state := update.InitState()

	for {
		state = update.Update(state, io(update.StateToOutput(state), serCh))
	}
}
