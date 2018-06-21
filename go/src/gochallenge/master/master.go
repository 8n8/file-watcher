package main

import (
	"goji.io"
	"goji.io/pat"
	"net/http"
	"io/ioutil"
	"fmt"
	"encoding/json"
	"sort"
	"strings"
)

type folderState struct {
	fileSet map[string]bool
	guidOfLastUpdate string
}

type stateT struct {
	folderFileSets map[string]folderState
	masterList []string
	keepGoing bool
	fatalErr error
	nonFatalErrs []error
	msgToWatcher msgToWatcherT
	respondToClient bool
	clientChan chan []byte
}

type msgToWatcherT struct {
	msg []byte
	ch chan []byte
}

type clientOrWatcherT bool

const (
	clientRequestC clientOrWatcherT = false
	watcherInputC clientOrWatcherT = true
)

type ioResultT struct {
	rawWatcherInput rawWatcherInputT
	clientRequest chan []byte
	clientOrWatcher clientOrWatcherT
}

type rawWatcherInputT struct {
	content []byte
	replyChan chan []byte
	bodyReadErr error
}

type responseToClientT struct {
	content []byte
	ch chan []byte
}

type outputT struct {
	responseToClient responseToClientT
	responseToWatcher msgToWatcherT
	msgToPrint string
}

func stateToOutput(state stateT) outputT {
	var result outputT
	nonFatalErrs := state.nonFatalErrs
	if state.respondToClient {
		msg, err := json.Marshal(state.masterList)
		nonFatalErrs = append(nonFatalErrs, err)
		if err == nil {
			result.responseToClient.content = msg
			result.responseToClient.ch = state.clientChan
		}
	}
	result.responseToWatcher = state.msgToWatcher
	result.msgToPrint = combineErrors(nonFatalErrs)
	return result
}

func combineErrors(errorList []error) string {
	var noNils []error
	for _, err := range errorList {
		if err != nil {
			noNils = append(noNils, err)
		}
	}
	if len(noNils) == 0 { return "" }
	return strings.Join(errorsToStrings(noNils), "\n")
}

func errorsToStrings(errors []error) []string {
	var strs []string
	for _, err := range errors {
		strs = append(strs, err.Error())
	}
	return strs
}

func io(output outputT, chs serverChannelsT) ioResultT {
	result := ioResultT{}
	if output.msgToPrint != "" {
		fmt.Println(output.msgToPrint)
	}
	if len(output.responseToClient.content) != 0 {
		output.responseToClient.ch <- output.responseToClient.content
	}
	if len(output.responseToWatcher.msg) != 0 {
		output.responseToWatcher.ch <- output.responseToWatcher.msg
	}
	select {
	case clientRequest := <-chs.clientRequest:
		result.clientRequest = clientRequest
		result.clientOrWatcher = clientRequestC
	case rawWatcherInput := <-chs.watcherInput:
		result.rawWatcherInput = rawWatcherInput
		result.clientOrWatcher = watcherInputC
		return result
	}
	return result
}

func decodeInput(raw []byte) (watcherInputT, error) {
	var watcherInput watcherInputT
	err := json.Unmarshal(raw, &watcherInput)
	return watcherInput, err
}

func handleWatcherInput(state stateT, ioResult ioResultT) stateT {
	if ioResult.rawWatcherInput.bodyReadErr != nil {
		return stateT{
			folderFileSets: state.folderFileSets,
			masterList: state.masterList,
			keepGoing: true,
			fatalErr: nil,
			nonFatalErrs: []error{ioResult.rawWatcherInput.bodyReadErr},
			msgToWatcher: msgToWatcherT{
				msg: []byte("internal error"),
				ch: ioResult.rawWatcherInput.replyChan,
			},
		}
	}
	watcherInput, err := decodeInput(ioResult.rawWatcherInput.content)
	if err != nil {
		return stateT{
			folderFileSets: state.folderFileSets,
			masterList: state.masterList,
			keepGoing: true,
			fatalErr: nil,
			nonFatalErrs: []error{err},
			msgToWatcher: msgToWatcherT{
				msg: []byte("could not decode json"),
				ch: ioResult.rawWatcherInput.replyChan,
			},
		}
	}
	if watcherInput.NewList {
		fileSets := state.folderFileSets
		fileSets[watcherInput.Directory] = newFolderState(
			watcherInput.AllFiles,
			watcherInput.Guid)
		return stateT{
			folderFileSets: fileSets,
			masterList: makeMasterList(fileSets),
			keepGoing: true,
			fatalErr: nil,
			nonFatalErrs: []error{},
			msgToWatcher: msgToWatcherT{
				msg: []byte("ok"),
				ch: ioResult.rawWatcherInput.replyChan,
			},
		}
	}
	if state.folderFileSets[watcherInput.Directory].guidOfLastUpdate != watcherInput.PreviousGuid {
		return stateT {
			folderFileSets: state.folderFileSets,
			masterList: state.masterList,
			keepGoing: true,
			fatalErr: nil,
			nonFatalErrs: []error{fmt.Errorf("mismatched guid for %s", watcherInput.Directory)},
			msgToWatcher: msgToWatcherT{
				msg: []byte("badGuid"),
				ch: ioResult.rawWatcherInput.replyChan,
			},
		}
	}
	fileSets := state.folderFileSets
	fileSets[watcherInput.Directory] = updateFolderState(
		state.folderFileSets[watcherInput.Directory].fileSet,
		watcherInput)
	return stateT{
		folderFileSets: fileSets,
		masterList: makeMasterList(fileSets),
		keepGoing: true,
		fatalErr: nil,
		nonFatalErrs: []error{},
		msgToWatcher: msgToWatcherT{
			msg: []byte("ok"),
			ch: ioResult.rawWatcherInput.replyChan,
		},
	}
}

func updateFolderState(current map[string]bool, watcherInput watcherInputT) folderState {
	newFileSet := current
	if watcherInput.ChangeType == "create" {
		newFileSet[watcherInput.FileName] = true
	}
	if watcherInput.ChangeType == "delete" {
		delete(newFileSet, watcherInput.FileName)
	}
	return folderState{
		fileSet: newFileSet,
		guidOfLastUpdate: watcherInput.Guid,
	}
}

func getKeys(m map[string]bool) []string {
	result := make([]string, len(m))
	i := 0
	for k := range m {
		result[i] = k
		i++
	}
	return result
}

func makeMasterList(folderFileSets map[string]folderState) []string {
	all := map[string]bool{}
	for _, v := range folderFileSets {
		for k, _ := range v.fileSet {
			all[k] = true
		}
	}
	asSlice := getKeys(all)
	sort.Strings(asSlice)
	return asSlice
}

func newFolderState(fileList []string, updateGuid string) folderState {
	fileSet := map[string]bool{}
	for _, filename := range fileList {
		fileSet[filename] = true
	}
	return folderState{
		fileSet: fileSet,
		guidOfLastUpdate: updateGuid,
	}
}

func update(state stateT, ioResult ioResultT) stateT {
	if ioResult.clientOrWatcher == watcherInputC {
		return handleWatcherInput(state, ioResult)
	}
	result := state
	result.respondToClient = true
	result.msgToWatcher = msgToWatcherT{
		msg: []byte{},
		ch: make(chan []byte),
	}
	result.clientChan = ioResult.clientRequest
	return result
}

type watcherInputT struct {
	Guid string
	PreviousGuid string
	FileName string
	ChangeType string
	AllFiles []string
	Directory string
	NewList bool
}

type serverChannelsT struct {
	clientRequest chan (chan []byte)
	watcherInput chan rawWatcherInputT
	err chan error
}

const maxBufferSize = 1000

func initState() stateT {
	return stateT{
		folderFileSets: map[string]folderState{},
		masterList:     []string{},
		keepGoing:      true,
		fatalErr: nil,
		nonFatalErrs: []error{},
		respondToClient: false,
		clientChan: nil,
	}
}

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
			resp := <-replyChan
			w.Write(resp)
		}
		mux := goji.NewMux()
		mux.HandleFunc(pat.Get("/files"), clientRequest)
		mux.HandleFunc(pat.Post("/"), watcherInput)
		http.ListenAndServe("localhost:3000", mux)
	}()

	state := initState()
	for state.keepGoing {
		state = update(state, io(stateToOutput(state), serCh))
	}
}
