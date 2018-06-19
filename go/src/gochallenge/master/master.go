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
	if len(watcherInput.completeList) != 0 {
		fileSets := state.folderFileSets
		fileSets[watcherInput.dirWatching] = newFolderState(
			watcherInput.completeList,
			watcherInput.guid)
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
	if state.folderFileSets[watcherInput.dirWatching].guidOfLastUpdate != watcherInput.guid {
		return stateT {
			folderFileSets: state.folderFileSets,
			masterList: state.masterList,
			keepGoing: true,
			fatalErr: nil,
			nonFatalErrs: []error{fmt.Errorf("mismatched guid for %s", watcherInput.dirWatching)},
			msgToWatcher: msgToWatcherT{
				msg: []byte("bad guid"),
				ch: ioResult.rawWatcherInput.replyChan,
			},
		}
	}
	fileSets := state.folderFileSets
	fileSets[watcherInput.dirWatching] = updateFolderState(
		state.folderFileSets[watcherInput.dirWatching].fileSet,
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
	for _, toCreate := range watcherInput.creations {
		newFileSet[toCreate] = true
	}
	for _, toDelete := range watcherInput.deletions {
		delete(newFileSet, toDelete)
	}
	return folderState{
		fileSet: newFileSet,
		guidOfLastUpdate: watcherInput.guid,
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
	result.clientChan = ioResult.clientRequest
	return result
}

type watcherInputT struct {
	previousGuid string
	guid string
	deletions []string
	creations []string
	completeList []string
	dirWatching string
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
		clientChan: make(chan []byte),
	}
}

func main() {
	fmt.Println("c")

	serCh := serverChannelsT{
		clientRequest: make(chan (chan []byte), maxBufferSize),
		watcherInput: make(chan rawWatcherInputT, maxBufferSize),
		err: make(chan error),
	}

	go func() {
		fmt.Println("d")
		clientRequest := func(w http.ResponseWriter, r *http.Request) {
			fmt.Println("a")
			if len(serCh.clientRequest) == maxBufferSize {
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte("Server too busya."))
				return
			}
			replyChan := make(chan []byte)
			fmt.Println("b")
			serCh.clientRequest <- replyChan
			w.Write(<-replyChan)
		}

		watcherInput := func(w http.ResponseWriter, r *http.Request) {
			fmt.Println("e")
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
		fmt.Println("f")
		mux := goji.NewMux()
		mux.HandleFunc(pat.Get("/files"), clientRequest)
		mux.HandleFunc(pat.Post("/"), watcherInput)
		http.ListenAndServe("localhost:3000", mux)
	}()

	state := initState()
	for state.keepGoing {
		fmt.Println("h")
		state = update(state, io(stateToOutput(state), serCh))
	}
}