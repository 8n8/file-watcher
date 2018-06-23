package main

import (
	"fmt"
	"gochallenge/master/update"
	"goji.io"
	"goji.io/pat"
	"io/ioutil"
	"net/http"
)

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

func main() {
	serCh := serverChannelsT{
		clientRequest: make(chan (chan []byte), maxBufferSize),
		watcherInput:  make(chan update.RawWatcherInputT, maxBufferSize),
		err:           make(chan error),
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
			serCh.watcherInput <- update.RawWatcherInputT{
				Content:     body,
				ReplyChan:   replyChan,
				BodyReadErr: err,
			}
			resp := <-replyChan
			w.Write(resp)
		}
		mux := goji.NewMux()
		mux.HandleFunc(pat.Get("/files"), clientRequest)
		mux.HandleFunc(pat.Post("/"), watcherInput)
		http.ListenAndServe("localhost:3000", mux)
	}()

	state := update.InitState()
	for {
		state = update.Update(state, io(update.StateToOutput(state), serCh))
	}
}

